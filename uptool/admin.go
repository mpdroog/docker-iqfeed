package main

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"time"
)

/** admin is the go-routine that monitors if the upstream
 * connection is Connected and else sends a Connect */
func admin() {
	init := true
	dur, e := time.ParseDuration("10s")
	if e != nil {
		slog.Error("admin[keepAlive parseDuration]", "e", e.Error())
		panic("DevErr")
	}

	for {
		if init == false {
			// Always sleep after first try
			time.Sleep(time.Second * 1)
		}
		init = false

		// wait for running
		for {
			name := "iqfeed"
			if _, ok := Running.Load(name); ok == true {
				// Service avail
				if Verbose {
					slog.Info("admin dep available", "name", name)
				}
				break
			}
			if Verbose {
				slog.Info("admin[await]", "name", name)
			}
			time.Sleep(time.Millisecond * 250)
		}

		if Verbose {
			slog.Info("admin[connect]")
		}
		// Keep alive conn
		conn, e := net.DialTimeout("tcp", "127.0.0.1:9300", defaultConnectTimeout)
		if e != nil {
			slog.Info("admin[Dial]", "e", e.Error())
			continue
		}

		c := bufio.NewReader(conn)

		// Check if conn working
		{
			deadline := time.Now().Add(dur)
			if e := conn.SetDeadline(deadline); e != nil {
				conn.Close()
				slog.Error("admin[setDeadline]", "e", e.Error())
				continue
			}

			if _, e := conn.Write([]byte("T\r\n")); e != nil {
				conn.Close() // TODO: err?
				slog.Error("admin[writeT]", "e", e.Error())
				continue
			}
			line, _, e := c.ReadLine()
			if e != nil {
				conn.Close() // TODO: err?
				slog.Error("admin[readlineT]", "e", e.Error())
				continue
			}
			if Verbose {
				slog.Info("admin[readlineT]", "line", line)
			}
		}

		for {
			deadline := time.Now().Add(dur)
			if e := conn.SetDeadline(deadline); e != nil {
				slog.Error("admin[for.setDeadline]", "e", e.Error())
				break
			}

			bin, _, e := c.ReadLine()
			bin = bytes.TrimSpace(bin)
			if e != nil {
				slog.Error("admin[readLine]", "e", e.Error())
				break
			}
			if Verbose {
				slog.Error("admin[readLine]", "bin", bin)
			}

			// S,STATS,,,0,0,1,0,0,0,,,Not Connected,6.2.0.25,\"490914\",0,0.0,0.0,0.08,0.08,0.08,
			if bytes.HasPrefix(bin, []byte("S,STATS")) {
				tok := bytes.SplitN(bin, []byte(","), 16)
				if bytes.Equal(tok[12], []byte("Not Connected")) {
					Running.Delete("admin")
				} else if bytes.Equal(tok[12], []byte("Connected")) {
					Running.Store("admin", struct{}{})
				}
			}
		}
		Running.Delete("admin")

		if e := conn.Close(); e != nil {
			slog.Error("admin[close]", "e", e.Error())
		}
	}
}
