package main

import (
	"bufio"
	"bytes"
	"log/slog"
	"net"
	"os"
	"time"
)

func killProcess(name string) error {
	v, ok := Running.Load("iqfeed")
	if !ok {
		// nothing to kill, not running probably
		return nil
	}

	// Kill instance
	pid := v.(int)
	p, e := os.FindProcess(pid)
	if e != nil {
		return e
	}
	if e := p.Kill(); e != nil {
		return e
	}
	//if Verbose {
	slog.Info("admin[readlineT] kill iqfeed", "pid", pid)
	//}\
	return nil
}

func initConn(dur time.Duration) (*PoolConn, error) {
	// is running?
	{
		_, ok := Running.Load("iqfeed")
		if !ok {
			return nil, nil
		}
		if Verbose {
			slog.Info("iqfeed-dep available")
		}
	}

	// test conn
	if Verbose {
		slog.Info("admin[connect]")
	}
	// Keep alive conn
	conn, e := net.DialTimeout("tcp", "127.0.0.1:9300", defaultConnectTimeout)
	if e != nil {
		return nil, e
	}

	r := bufio.NewReader(conn)

	// Check if conn working
	{
		deadline := time.Now().Add(dur)
		if e := conn.SetDeadline(deadline); e != nil {
			conn.Close()
			return nil, e
		}

		if _, e := conn.Write([]byte("T\r\n")); e != nil {
			conn.Close() // TODO: err?
			return nil, e
		}
		line, _, e := r.ReadLine()
		if e != nil {
			conn.Close() // TODO: err?
			return nil, e
		}

		if Verbose {
			slog.Info("admin[readlineT]", "line", line)
		}
	}

	return &PoolConn{
		C: conn, R: r,
	}, nil
}

/** admin is the go-routine that monitors if the upstream
 * connection is Connected and else sends a Connect */
func admin() {
	dur, e := time.ParseDuration("10s")
	if e != nil {
		slog.Error("admin[keepAlive parseDuration]", "e", e.Error())
		panic("DevErr")
	}

	failCounter := 0
	for {
		// Always delay 1sec so we don't flood the admin-conn and prevent kill frenzies
		time.Sleep(time.Second * 1)

		pconn, e := initConn(dur)
		if e != nil {
			slog.Info("admin[initConn]", "e", e.Error())

			failCounter++
			if failCounter == 10 {
				// Failed 10 times (for 10sec)
				if e := killProcess("iqfeed"); e != nil {
					slog.Error("admin[killProcess]", "e", e.Error())
				}
			}
			continue
		}
		if pconn == nil {
			// TODO: Also include in error handler for kill?
			slog.Info("admin[initConn] conn not yet ready")
			continue
		}

		// reset counter
		failCounter = 0
		for {
			if e := pconn.IncreaseDeadline(dur); e != nil {
				slog.Error("admin[for.setDeadline]", "e", e.Error())
				break
			}

			bin, e := pconn.ReadLine()
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

		if e := pconn.C.Close(); e != nil {
			slog.Error("admin[close]", "e", e.Error())
		}
	}
}
