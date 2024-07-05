package main

import (
	"bufio"
	"bytes"
	"fmt"
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
			fmt.Printf("ConnInit stream<< %s\n", bin)
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

func ConnTest(upConn net.Conn) error {
	if _, e := upConn.Write([]byte("S,TEST\r\n")); e != nil {
		return e
	}

	rUp := bufio.NewReader(upConn)
	{
		bin, e := rUp.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
		if Verbose {
			fmt.Printf("ConnTest stream<< %s\n", bin)
		}
		if e != nil {
			return e
		}
		if !bytes.Equal(bin, []byte("E,!SYNTAX_ERROR!,")) {
			return fmt.Errorf("[upConn Equal] invalid res=%s\n", bin)
		}
	}

	return nil
}

func ConnKeepAlive() {
	for {
		time.Sleep(40 * time.Second)
		mutex.Lock()
		if Verbose {
			fmt.Printf("GetConn::start\n")
		}

		for k, conn := range conns {
			deadline := time.Now().Add(time.Second * 2)
			if e := conn.SetDeadline(deadline); e != nil {
				fmt.Printf("ConnKeepAlive e=%s\n", e.Error())
				delete(conns, k)
				continue
			}

			if e := ConnTest(conn); e != nil {
				fmt.Printf("ConnKeepAlive(test) e=%s\n", e.Error())
				delete(conns, k)
				continue
			}
		}

		if Verbose {
			fmt.Printf("GetConn::finish\n")
		}
		mutex.Unlock()
	}
}

func GetConn() (net.Conn, error) {
	var conn net.Conn

	mutex.Lock()
	// random conn trick
	for k, randomConn := range conns {
		conn = randomConn
		delete(conns, k)
		mutex.Unlock()

		deadline := time.Now().Add(deadlineStream)
		if e := conn.SetDeadline(deadline); e != nil {
			return nil, e
		}

		// Ensure the conn is good
		if e := ConnTest(conn); e != nil {
			fmt.Printf("GetConn(test) e=%s\n", e.Error())
			delete(conns, k)
			continue
		}

		return conn, nil
	}
	mutex.Unlock()

	upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
	if e != nil {
		return nil, e
	}
	if e := ConnInit(upConn); e != nil {
		return nil, e
	}

	deadline := time.Now().Add(deadlineStream)
	if e := upConn.SetDeadline(deadline); e != nil {
		return nil, e
	}
	// Ensure the conn is good
	if e := ConnTest(conn); e != nil {
		fmt.Printf("GetConn(new-test) e=%s\n", e.Error())
		upConn.Close() // ignore any error
		continue
	}

	return upConn, nil
}

func FreeConn(n net.Conn) {
	// Ensure the conn is good before we offer it again
	if e := ConnTest(n); e != nil {
		fmt.Printf("FreeConn(test) e=%s\n", e.Error())
		return
	}

	mutex.Lock()
	counter++
	conns[fmt.Sprintf("%d", counter)] = n
	mutex.Unlock()
}
