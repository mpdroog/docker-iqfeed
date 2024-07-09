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

var (
	conns   map[string]net.Conn
	counter int
	mutex   *sync.Mutex
)

func ConnKeepAliveInit() {
	mutex = new(sync.Mutex)
	conns = make(map[string]net.Conn)
	go ConnKeepAlive()
}

func ConnInit(upConn net.Conn) error {
	if _, e := upConn.Write([]byte("S,SET PROTOCOL,6.2\r\n")); e != nil {
		return e
	}

	rUp := bufio.NewReader(upConn)
	// S,CURRENT PROTOCOL,6.2
	{
		bin, e := rUp.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
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

func ConnTest(upConn net.Conn, origin string) error {
	if _, e := upConn.Write([]byte("S,TEST\r\n")); e != nil {
		return e
	}

	rUp := bufio.NewReader(upConn)
	// Iterate on conn until any 'old data' is flushed
	flushed := 0
	for i := 0; i < 10000; i++ {
		bin, e := rUp.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
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

		slog.Warn("tcp_pool(ConnTest) remaining data", "bin", bin)
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
			deadline := time.Now().Add(deadlineCmd)
			if e := conn.SetDeadline(deadline); e != nil {
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

func readConnCache() (*net.Conn) {
	mutex.Lock()
	defer mutex.Unlock()

	if len(conns) == 0 {
		return nil
	}

	// pick 'random' conn
	for k, randomConn := range conns {
		conn := randomConn
		delete(conns, k)
		return &conn
	}

	return nil
}

func GetConn() (net.Conn, error) {
	// 1. From pool
	for {
		cp := readConnCache()
		if cp == nil {
			// No connection avail, allow to create new conn
			break
		}

		conn := *cp
		deadline := time.Now().Add(deadlineStream)
		if e := conn.SetDeadline(deadline); e != nil {
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

		deadline := time.Now().Add(deadlineStream)
		if e := upConn.SetDeadline(deadline); e != nil {
			upConn.Close() // ignore any error
			return nil, e
		}

		if e := ConnInit(upConn); e != nil {
			upConn.Close() // ignore any error
			return nil, e
		}

		return upConn, nil
	}
}

func FreeConn(n net.Conn) {
	deadline := time.Now().Add(time.Second * 2)
	if e := n.SetDeadline(deadline); e != nil {
		slog.Error("tcp_pool(FreeConn) setDeadline", "e", e.Error())
		return
	}

	// Ensure the conn is good before we add it to the pool of conns
	if e := ConnTest(n, "FreeConn"); e != nil {
		slog.Error("tcp_pool(FreeConn) ConnTest", "e", e.Error())
		return
	}

	mutex.Lock()
	counter++
	conns[fmt.Sprintf("%d", counter)] = n
	mutex.Unlock()
}
