package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"time"
)

/** admin is the go-routine that monitors if the upstream
 * connection is Connected and else sends a Connect */
func admin() {
	init := true
	dur, e := time.ParseDuration("10s")
	if e != nil {
		fmt.Printf("[keepAlive parseDuration]: %s\n", e.Error())
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
					fmt.Printf("[%s] dep avail\n", name)
				}
				break
			}
			if Verbose {
				fmt.Printf("[admin] await %s\n", name)
			}
			time.Sleep(time.Millisecond * 250)
		}

		if Verbose {
			fmt.Printf("[admin] connect\n")
		}
		// Keep alive conn
		conn, e := net.DialTimeout("tcp", "127.0.0.1:9300", defaultConnectTimeout)
		if e != nil {
			fmt.Printf("[admin.Dial] e=%s\n", e.Error())
			continue
		}

		c := bufio.NewReader(conn)

		// Check if conn working
		{
			deadline := time.Now().Add(dur)
			if e := conn.SetDeadline(deadline); e != nil {
				conn.Close()
				fmt.Printf("[admin setDeadline]: %s\n", e.Error())
				continue
			}

			if _, e := conn.Write([]byte("T\r\n")); e != nil {
				conn.Close() // TODO: err?
				fmt.Printf("[admin.WriteT] e=%s\n", e.Error())
				continue
			}
			line, _, e := c.ReadLine()
			if e != nil {
				conn.Close() // TODO: err?
				fmt.Printf("[admin.ReadLineT] e=%s\n", e.Error())
				continue
			}
			if Verbose {
				fmt.Printf("[admin.ReadLineT] %s\n", line)
			}
		}

		for {
			deadline := time.Now().Add(dur)
			if e := conn.SetDeadline(deadline); e != nil {
				fmt.Printf("[admin for->setDeadline]: %s\n", e.Error())
				break
			}

			bin, _, e := c.ReadLine()
			bin = bytes.TrimSpace(bin)
			if e != nil {
				fmt.Printf("[admin.ReadLine] e=%s\n", e.Error())
				break
			}
			if Verbose {
				fmt.Printf("[admin.ReadLine] %s\n", bin)
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
			fmt.Printf("[admin.Close] e=%s\n", e.Error())
		}
	}
}
