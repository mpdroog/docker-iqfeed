package main

import (
	"bufio"
	"bytes"
	"fmt"
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
		fmt.Printf("[keepAlive parseDuration]: %s\n", e.Error())
		panic("DevErr")
	}

	for {
		time.Sleep(time.Second * 1)

		if Verbose {
			fmt.Printf("[keepalive] for.Next")
		}

		if _, ok := Running.Load("iqfeed"); !ok {
			fmt.Printf("[keepalive] iqfeed not running\n")
			continue
		}
		if _, ok := Running.Load("admin"); !ok {
			fmt.Printf("[keepalive] admin-conn not running\n")
			continue
		}

		upConn, e := net.DialTimeout("tcp", upAddr, defaultConnectTimeout)
		if e != nil {
			fmt.Printf("[keepAlive dial]: %s\n", e.Error())
			continue
		}
		r := bufio.NewReader(upConn)

		deadline := time.Now().Add(dur)
		if e := upConn.SetDeadline(deadline); e != nil {
			upConn.Close()
			fmt.Printf("[keepalive setDeadline]: %s\n", e.Error())
			continue
		}

		if _, e := upConn.Write([]byte("S,SET PROTOCOL,6.2\r\n")); e != nil {
			fmt.Printf("[upConn write] %s\n", e.Error())
			continue
		}
		if _, e := upConn.Write([]byte("S,SET CLIENT NAME,KEEPALIVE\r\n")); e != nil {
			fmt.Printf("[upConn write] %s\n", e.Error())
			continue
		}

		// Keep upstream alive
		for {
			deadline := time.Now().Add(dur)
			if e := upConn.SetDeadline(deadline); e != nil {
				upConn.Close()
				fmt.Printf("[keepalive setDeadline]: %s\n", e.Error())
				continue
			}

			// Request timestamp
			if _, e := upConn.Write([]byte("T\r\n")); e != nil {
				upConn.Close()
				fmt.Printf("[keepalive Write]: %s\n", e.Error())
				break
			}

			// line=T,20230530 05:58:26
			bin, e := r.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				fmt.Printf("line=%s\n", bin)
			}
			if e != nil {
				upConn.Close()
				fmt.Printf("[keepalive ReadBytes]: %s\n", e.Error())
				break
			}

			if Verbose {
				fmt.Printf("[keepalive] success.Next\n")
			}
			time.Sleep(time.Second * 1)
		}

		upConn.Close()
	}
}
