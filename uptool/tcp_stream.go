package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/maurice2k/tcpserver"
)

// streamIdleTimeout closes idle streaming connections to prevent goroutine leaks
const streamIdleTimeout = 30 * time.Second

// timeoutReader wraps a reader and extends the connection deadline on each read
type timeoutReader struct {
	r    io.Reader
	conn net.Conn
}

func (t *timeoutReader) Read(p []byte) (n int, err error) {
	n, err = t.r.Read(p)
	if n > 0 {
		t.conn.SetDeadline(time.Now().Add(streamIdleTimeout))
	}
	return
}

// streamProxy creates a bidirectional proxy for streaming connections (L1/L2).
// Unlike the lookup proxy, this doesn't wait for !ENDMSG! - it just forwards
// data in both directions until either side closes.
func streamProxy(conn tcpserver.Connection, upstreamAddr string, name string) {
	defer func() {
		conn.Close() // safe to call even if already closed
		if Verbose {
			slog.Info("tcp_stream dropped conn", "name", name)
		}
	}()

	if Verbose {
		slog.Info("tcp_stream new conn", "name", name, "remote", conn.RemoteAddr())
	}

	// Check if iqfeed and admin are ready
	if _, ok := Running.Load("iqfeed"); !ok {
		slog.Warn("tcp_stream rejected - iqfeed not running", "name", name)
		conn.Write([]byte("E,NO_DAEMON\r\n"))
		return
	}
	if _, ok := Running.Load("admin"); !ok {
		slog.Warn("tcp_stream rejected - admin not ready", "name", name)
		conn.Write([]byte("E,NO_ADMIN\r\n"))
		return
	}

	// Connect to upstream
	upstream, e := net.DialTimeout("tcp", upstreamAddr, defaultConnectTimeout)
	if e != nil {
		slog.Error("tcp_stream upstream dial", "name", name, "addr", upstreamAddr, "e", e.Error())
		conn.Write([]byte(fmt.Sprintf("E,UPSTREAM_CONN,%s\r\n", e.Error())))
		return
	}

	if Verbose {
		slog.Info("tcp_stream connected to upstream", "name", name, "addr", upstreamAddr)
	}

	// Set initial idle timeout on both connections
	deadline := time.Now().Add(streamIdleTimeout)
	conn.SetDeadline(deadline)
	upstream.SetDeadline(deadline)

	// done channel signals when either copy finishes
	done := make(chan struct{})

	// Bidirectional copy with goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	// Client -> Upstream (extends upstream deadline on activity)
	go func() {
		defer wg.Done()
		_, e := io.Copy(upstream, &timeoutReader{r: conn, conn: upstream})
		if e != nil && Verbose {
			slog.Info("tcp_stream client->upstream closed", "name", name, "e", e.Error())
		}
		select {
		case done <- struct{}{}:
		default:
		}
	}()

	// Upstream -> Client (extends client deadline on activity)
	go func() {
		defer wg.Done()
		_, e := io.Copy(conn, &timeoutReader{r: upstream, conn: conn})
		if e != nil && Verbose {
			slog.Info("tcp_stream upstream->client closed", "name", name, "e", e.Error())
		}
		select {
		case done <- struct{}{}:
		default:
		}
	}()

	// Wait for either direction to finish, then close both to unblock the other
	<-done
	conn.Close()
	upstream.Close()

	wg.Wait()
	if Verbose {
		slog.Info("tcp_stream connection finished", "name", name)
	}
}

// level1Proxy handles Level 1 streaming quote connections
func level1Proxy(conn tcpserver.Connection) {
	streamProxy(conn, "127.0.0.1:5009", "L1")
}

// level2Proxy handles Level 2 market depth connections
func level2Proxy(conn tcpserver.Connection) {
	streamProxy(conn, "127.0.0.1:9200", "L2")
}

// startStreamServer starts a TCP server for streaming data
func startStreamServer(addr string, handler func(tcpserver.Connection), name string) {
	server, e := tcpserver.NewServer(addr)
	if e != nil {
		slog.Error("tcpserver.NewServer", "name", name, "addr", addr, "e", e.Error())
		return
	}

	server.SetRequestHandler(handler)
	if e := server.Listen(); e != nil {
		slog.Error("tcpserver.Listen", "name", name, "addr", addr, "e", e.Error())
		return
	}

	slog.Info("tcp_stream server started", "name", name, "addr", addr)
	server.Serve()
}
