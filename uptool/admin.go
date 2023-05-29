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

		// TODO: Timeouts?
		if Verbose {
			fmt.Printf("[admin] connect\n")
		}
		// Keep alive conn
		conn, e := net.Dial("tcp", "127.0.0.1:9300")
		if e != nil {
			fmt.Printf("[admin.Dial] e=%s\n", e.Error())
			continue
		}

		c := bufio.NewReader(conn)

		// Check if conn working
		{
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

		connectCount := 0
		firstPkg := true
		for {
			line, _, e := c.ReadLine()
			if e != nil {
				fmt.Printf("[admin.WriteTS] e=%s\n", e.Error())
				break
			}
			if Verbose {
				fmt.Printf("[admin.ReadLine] %s\n", line)
			}

			// S,STATS,,,0,0,1,0,0,0,,,Not Connected,6.2.0.25,\"490914\",0,0.0,0.0,0.08,0.08,0.08,
			if bytes.HasPrefix(line, []byte("S,STATS")) {
				tok := bytes.SplitN(line, []byte(","), 16)
				if bytes.Equal(tok[12], []byte("Not Connected")) {
					if _, e := conn.Write([]byte("S,CONNECT\r\n")); e != nil {
						fmt.Printf("[admin.Write CONNECT] e=%s\n", e.Error())
						break
					}
					if Verbose {
						fmt.Printf("[admin.Write] >> S,CONNECT\n")
					}
					if connectCount >= 10 {
						fmt.Printf("[admin.Timeout] failed connecting (10 attempts)\n")
						break
					}
					connectCount++
				} else if firstPkg && bytes.Equal(tok[12], []byte("Connected")) {
					firstPkg = false
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
