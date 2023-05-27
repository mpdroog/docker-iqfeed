package main

import (
	"io"
	"testing"
	"fmt"
	"github.com/maurice2k/tcpserver"
)

func TestTCP(t *testing.T) {
	server, e := tcpserver.NewServer(":9100")
	if e != nil {
		fmt.Println(e)
		return
	}

	server.SetRequestHandler(handleConn)
	server.Listen()
	server.Serve()
}

func requestHandler(conn tcpserver.Connection) {
    io.Copy(conn, conn)
}