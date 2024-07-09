package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

type PoolConn struct {
	C net.Conn
	R *bufio.Reader
}

func (p *PoolConn) ReadLine() ([]byte, error) {
	bin, e := p.R.ReadBytes(byte('\n'))
	bin = bytes.TrimSpace(bin)
	if e != nil && len(bin) > 0 {
		slog.Warn("tcp_pool(conn.ReadLine) dropped", "bin", string(bin))
	}
	return bin, e
}
func (p *PoolConn) WriteLine(str []byte) (int, error) {
	return p.C.Write(append(str, []byte("\r\n")...))
}
func (p *PoolConn) IncreaseDeadline(deadline time.Duration) error {
	if e := p.C.SetDeadline(time.Now().Add(deadline)); e != nil {
		return e
	}
	return nil
}

var (
	conns   map[string]*PoolConn
	counter int
	mutex   *sync.Mutex
)

func ConnKeepAliveInit() {
	mutex = new(sync.Mutex)
	conns = make(map[string]*PoolConn)
	go ConnKeepAlive()
}

func ConnInit(conn *PoolConn) error {
	if _, e := conn.WriteLine([]byte("S,SET PROTOCOL,6.2")); e != nil {
		return e
	}

	// S,CURRENT PROTOCOL,6.2
	{
		bin, e := conn.ReadLine()
		if Verbose {
			slog.Info("tcp_pool(ConnInit)", "stream", bin)
		}
		if e != nil {
			return e
		}
		if !bytes.Equal(bin, []byte("S,CURRENT PROTOCOL,6.2")) {
			return fmt.Errorf("[upConn Equal] invalid res=%s\n", bin)
		}
	}

	return nil
}

func ConnTest(conn *PoolConn, origin string) error {
	if _, e := conn.WriteLine([]byte("S,TEST")); e != nil {
		return e
	}

	// Iterate on conn until any 'old data' is flushed
	flushed := 0
	for i := 0; i < 10000; i++ {
		bin, e := conn.ReadLine()
		if Verbose {
			slog.Info("tcp_pool(ConnTest)", "stream", bin)
		}
		if e != nil {
			return e
		}
		if bytes.Equal(bin, []byte("E,!SYNTAX_ERROR!,")) {
			if flushed > 0 {
				slog.Warn("tcp_pool(ConnTest) remaining data", "origin", origin, "n", flushed)
			}

			// reached end of data
			return nil
		}

		slog.Warn("tcp_pool(ConnTest) remaining data", "bin", string(bin))
		flushed++
	}
	return fmt.Errorf("ConnTest exhausted conn.read")
}

func ConnKeepAlive() {
	for {
		time.Sleep(40 * time.Second)
		mutex.Lock()
		if Verbose {
			slog.Info("tcp_pool(ConnKeepAlive) start")
		}

		for k, conn := range conns {
			if e := conn.IncreaseDeadline(deadlineCmd); e != nil {
				slog.Error("tcp_pool(ConnKeepAlive) SetDeadline", "e", e.Error())
				delete(conns, k)
				continue
			}

			if e := ConnTest(conn, "ConnKeepAlive"); e != nil {
				slog.Error("tcp_pool(ConnKeepAlive) ConnTest", "e", e.Error())
				delete(conns, k)
				continue
			}
		}

		if Verbose {
			slog.Info("tcp_pool(ConnKeepAlive) finish")
		}
		mutex.Unlock()
	}
}

func readConnCache() *PoolConn {
	if len(conns) == 0 {
		return nil
	}

	mutex.Lock()
	defer mutex.Unlock()

	// pick 'random' conn
	for k, randomConn := range conns {
		conn := randomConn
		delete(conns, k)
		return conn
	}

	return nil
}

func GetConn() (*PoolConn, error) {
	// 1. From pool
	for {
		conn := readConnCache()
		if conn == nil {
			// No connection avail, allow to create new conn
			break
		}

		if e := conn.IncreaseDeadline(deadlineCmd); e != nil {
			slog.Warn("tcp_pool(GetConn) setDeadline", "e", e)
			continue
		}

		// Ensure the conn is good
		if e := ConnTest(conn, "GetConn"); e != nil {
			slog.Warn("tcp_pool(GetConn) ConnTest", "e", e)
			continue
		}
		return conn, nil
	}

	// 2. new conn
	{
		upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
		if e != nil {
			return nil, e
		}

		conn := &PoolConn{C: upConn, R: bufio.NewReader(upConn)}
		if e := conn.IncreaseDeadline(deadlineCmd); e != nil {
			upConn.Close() // ignore any error
			return nil, e
		}

		if e := ConnInit(conn); e != nil {
			upConn.Close() // ignore any error
			return nil, e
		}

		return conn, nil
	}
}

func FreeConn(n *PoolConn) {
	if e := n.IncreaseDeadline(deadlineCmd); e != nil {
		n.C.Close()
		slog.Error("tcp_pool(FreeConn) setDeadline", "e", e.Error())
		return
	}

	// Ensure the conn is good before we add it to the pool of conns
	if e := ConnTest(n, "FreeConn"); e != nil {
		n.C.Close()
		slog.Error("tcp_pool(FreeConn) ConnTest", "e", e.Error())
		return
	}

	mutex.Lock()
	counter++
	conns[fmt.Sprintf("%d", counter)] = n
	mutex.Unlock()
}
