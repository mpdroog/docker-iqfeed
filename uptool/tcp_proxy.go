package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/maurice2k/tcpserver"
	"net"
	"time"
)

/** defaultConnectTimeout is the default upstream.Connect timeout */
const defaultConnectTimeout = 3 * time.Second

/** EOM is End Of Message stream */
const EOM = "!ENDMSG!,"

/** streamReplies are all cmds we expect more than 1 result (till EOM) */
var streamReplies map[string]struct{}

/** init prepares tcp_proxy vars */
func init() {
	// Streaming cmds
	streamReplies = map[string]struct{}{
		"HDX": struct{}{},
		"HWX": struct{}{},
		"HMX": struct{}{},
		"HTD": struct{}{},
		"HTT": struct{}{},
		"HIX": struct{}{},
		"HID": struct{}{},
		"HIT": struct{}{},
		"HDT": struct{}{},
	}
}

/** tcpProxy is small conn.Accept handler that prepares upstream and
 * proxy's commands from the client to upstream */
func tcpProxy(conn tcpserver.Connection) {
	defer func() {
		if e := conn.Close(); e != nil {
			fmt.Errorf("handleConn.Close: %s\n", e.Error())
		}
		if Verbose {
			fmt.Printf("handleConn: dropped conn\n")
		}
	}()
	if Verbose {
		fmt.Printf("handleConn: new req\n")
	}

	if _, ok := Running.Load("iqfeed"); !ok {
		conn.Write([]byte("E,NO_DAEMON\r\n"))
		return
	}
	if _, ok := Running.Load("admin"); !ok {
		conn.Write([]byte("E,NO_ADMIN\r\n"))
		return
	}

	// Start the clock
	dur, e := time.ParseDuration("5s")
	if e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,PARSE_DURATION\r\n"))
		return
	}
	deadline := time.Now().Add(dur)

	if e := conn.SetDeadline(deadline); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,CONN_SET_DEADLINE\r\n"))
		return
	}

	upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
	if e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,UPSTREAM CONN_TIMEOUT\r\n"))
		return
	}
	defer upConn.Close()

	// Test if upstream conn is usable
	{
		if _, e := upConn.Write([]byte("TEST\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM_T\r\n"))
			return
		}
		b := make([]byte, len("E,!SYNTAX_ERROR!,\r\n"))
		if _, e := upConn.Read(b); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM_T_RES\r\n"))
			return
		}
		if !bytes.Equal(b, []byte("E,!SYNTAX_ERROR!,\r\n")) {
			fmt.Printf("handleConn: invalid res=%s\n", b)
			conn.Write([]byte("E,UPSTREAM_T_INV\r\n"))
			return
		}
	}

	if e := upConn.SetDeadline(deadline); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		conn.Write([]byte("E,UPSTREAM SET_DEADLINE\r\n"))
		return
	}

	// Inform client the connection is ready
	if _, e := conn.Write([]byte("READY\r\n")); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		return
	}

	// Own proxy
	r := bufio.NewReader(conn)
	r2 := bufio.NewReader(upConn)

	for {
		if Verbose {
			fmt.Printf("[proxy.next]\n")
		}

		// 0. timeouts
		// increase timeout upon receiving data
		deadline := time.Now().Add(dur)

		if e := conn.SetDeadline(deadline); e != nil {
			fmt.Errorf("conn.SetDeadline e=%s\n", e.Error())
			conn.Write([]byte("E,CONN_SET_DEADLINE\r\n"))
			break
		}
		if e := upConn.SetDeadline(deadline); e != nil {
			fmt.Errorf("upConn.SetDeadline e=%s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM SET_DEADLINE\r\n"))
			break
		}

		// 1. client cmd
		bin, e := r.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
		if e != nil {
			fmt.Errorf("conn.ReadBytes e=%s\n", e.Error())
			conn.Write([]byte("E,CONN_READ_CMD\r\n"))
			break
		}

		sep := bytes.Index(bin, []byte(","))
		cmd := bin
		if sep != -1 {
			cmd = bin[:sep]
		}
		cmd = bytes.ToUpper(cmd)
		if Verbose {
			fmt.Printf("<< %s -> %s\n", bin, cmd)
		}

		if bytes.Equal(cmd, []byte("QUIT")) {
			if Verbose {
				fmt.Errorf("QUIT-cmd\n")
			}
			break
		}

		if _, e := upConn.Write(bin); e != nil {
			fmt.Errorf("upConn.Write e=%s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM_W\r\n"))
			break
		}
		if _, e := upConn.Write([]byte("\r\n")); e != nil {
			fmt.Errorf("upConn.Write e=%s\n", e.Error())
			conn.Write([]byte("E,UPSTREAM_W\r\n"))
			break
		}

		// 2. stream?
		if _, isStream := streamReplies[string(cmd)]; isStream {
			for {
				if Verbose {
					fmt.Printf("Stream.next\n")
				}

				// read until EOM
				bin, e := r2.ReadBytes(byte('\n'))
				bin = bytes.TrimSpace(bin)
				if Verbose {
					fmt.Printf("stream<< %s\n", bin)
				}
				if e != nil {
					fmt.Errorf("upConn.Read e=%s\n", e.Error())
					conn.Write([]byte("UPSTREAM_R\r\n"))
					break
				}
				if _, e := conn.Write(bin); e != nil {
					fmt.Errorf("conn.Write e=%s\n", e.Error())
					break
				}
				if _, e := conn.Write([]byte("\r\n")); e != nil {
					fmt.Errorf("conn.Write e=%s\n", e.Error())
					break
				}
				if bytes.Equal(bin, []byte(EOM)) {
					// Done!
					if Verbose {
						fmt.Printf("End-Of-Stream\n")
					}
					break
				}
			}
		} else {
			// One reply
			bin, e := r2.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				fmt.Printf("line<< %s\n", bin)
			}

			if e != nil {
				fmt.Errorf("upConn.Read e=%s\n", e.Error())
				conn.Write([]byte("UPSTREAM_R\r\n"))
				break
			}
			if _, e := conn.Write(bin); e != nil {
				fmt.Errorf("conn.Write e=%s\n", e.Error())
				break
			}
			if _, e := conn.Write([]byte("\r\n")); e != nil {
				fmt.Errorf("conn.Write e=%s\n", e.Error())
				break
			}
		}
	}
}
