package main

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"time"
)

// keepalive tries to keep the IQFeed-daemon running by
// doing foreach send(TEST) > recv(ERR)
//
// DevNote: Be careful with this keepalive-code, it needs to manually
// call upConn.Close on every error!
func keepalive(upAddr string) {
	time.Sleep(time.Second * 10)
	dur, e := time.ParseDuration("10s")
	if e != nil {
		slog.Error("keepalive parseDuration", "e", e.Error())
		panic("DevErr")
	}

	for {
		time.Sleep(time.Second * 30)

		if Verbose {
			slog.Info("keepalive forNext")
		}

		if _, ok := Running.Load("iqfeed"); !ok {
			slog.Info("keepalive iqfeed not running")
			continue
		}
		if _, ok := Running.Load("admin"); !ok {
			slog.Info("keepalive admin-conn not running")
			continue
		}

		upConn, e := net.DialTimeout("tcp", upAddr, defaultConnectTimeout)
		if e != nil {
			slog.Error("keepalive dial", "e", e.Error())
			continue
		}
		r := bufio.NewReader(upConn)

		deadline := time.Now().Add(dur)
		if e := upConn.SetDeadline(deadline); e != nil {
			upConn.Close()
			slog.Error("keepalive setDeadline", "e", e.Error())
			continue
		}

		if _, e := upConn.Write([]byte("S,SET PROTOCOL,6.2\r\n")); e != nil {
			upConn.Close()
			slog.Error("keepalive upConnWriteProtocol", "e", e.Error())
			continue
		}
		if _, e := upConn.Write([]byte("S,SET CLIENT NAME,KEEPALIVE\r\n")); e != nil {
			upConn.Close()
			slog.Error("keepalive upConnWriteName", "e", e.Error())
			continue
		}

		// Keep upstream alive
		for {
			deadline := time.Now().Add(dur)
			if e := upConn.SetDeadline(deadline); e != nil {
				slog.Error("keepalive forSetDeadline", "e", e.Error())
				break
			}

			// Request timestamp
			if _, e := upConn.Write([]byte("T\r\n")); e != nil {
				slog.Error("keepalive write", "e", e.Error())
				break
			}

			// line=T,20230530 05:58:26
			bin, e := r.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				slog.Info("keepalive", "line", bin)
			}
			if e != nil {
				slog.Error("keepalive readBytes", "e", e.Error())
				break
			}

			if Verbose {
				slog.Info("keepalive successNext")
			}
			time.Sleep(time.Second * 30)
		}

		if e := upConn.Close(); e != nil {
			slog.Error("keepalive Close", "e", e.Error())
		}
	}
}
