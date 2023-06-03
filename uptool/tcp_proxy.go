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

/** loopLimit is the max for the iteration */
const loopLimit = 10000

/** EOM is End Of Message stream */
const EOM = "!ENDMSG!,"

/** streamReplies are all cmds we expect more than 1 result (till EOM) */
var streamReplies map[string]struct{}

var deadlineCmd time.Duration
var deadlineStream time.Duration

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

	var e error
	deadlineCmd, e = time.ParseDuration("5s")
	if e != nil {
		panic(e)
	}
	deadlineStream, e = time.ParseDuration("15s")
	if e != nil {
		panic(e)
	}
}

func isError(bin []byte) [][]byte {
	if bytes.HasPrefix(bin, []byte("E,")) {
		// Error
		// i.e. "E,!NO_DATA!,,", "E,Unauthorized user ID.,"
		buf := bytes.SplitN(bin, []byte(","), 4)
		return buf
	}

	return [][]byte{}
}

// LineFunc is called on every line read and stops the proxy on error
type LineFunc func(line []byte) error

// proxy opens an upstream connection and calls cb on every line it reads
func proxy(cmd string, cb LineFunc) error {
	if _, ok := Running.Load("iqfeed"); !ok {
		return fmt.Errorf("iqfeed not running")
	}
	if _, ok := Running.Load("admin"); !ok {
		return fmt.Errorf("admin not ready")
	}

	// TODO: Maybe re-use some pool?
	upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
	if e != nil {
		return e
	}
	defer func() {
		if e := upConn.Close(); e != nil {
			fmt.Printf("WARN: proxy defer.Close e=%s\n", e.Error())
		}
	}()

	deadline := time.Now().Add(deadlineStream)
	if e := upConn.SetDeadline(deadline); e != nil {
		return e
	}

	rUp := bufio.NewReader(upConn)

	// Prepare conn
	{
		if _, e := upConn.Write([]byte("S,SET PROTOCOL,6.2\r\n")); e != nil {
			return e
		}

		// S,CURRENT PROTOCOL,6.2
		{
			bin, e := rUp.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				fmt.Printf("stream<< %s\n", bin)
			}
			if e != nil {
				return e
			}
			if !bytes.Equal(bin, []byte("S,CURRENT PROTOCOL,6.2")) {
				return fmt.Errorf("[upConn Equal] invalid res=%s\n", bin)
			}
		}
	}

	if _, e := upConn.Write([]byte(cmd + "\r\n")); e != nil {
		return e
	}

	i := 0
	for ; i < loopLimit+1; i++ {
		// read until EOM
		bin, e := rUp.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
		if Verbose {
			fmt.Printf("stream<< %s\n", bin)
		}
		if e != nil {
			return e
		}

		if tok := isError(bin); len(tok) > 0 {
			if len(tok) == 0 {
				return fmt.Errorf("%s", tok)
			}
			return fmt.Errorf("%s", tok[1])
		}

		if bytes.Equal(bin, []byte(EOM)) {
			// Done!
			if Verbose {
				fmt.Printf("End-Of-Stream\n")
			}
			break
		}

		if e := cb(bin); e != nil {
			return e
		}
	}
	if i == loopLimit {
		return fmt.Errorf("CRIT: loopLimit(%d) reached, something wrong in code?\n", loopLimit)
	}

	return nil
}

/** tcpProxy is small conn.Accept handler that prepares upstream and
 * proxy's commands from the client to upstream */
func tcpProxy(conn tcpserver.Connection) {
	defer func() {
		if e := conn.Close(); e != nil {
			fmt.Printf("handleConn.Close: %s\n", e.Error())
		}
		if Verbose {
			fmt.Printf("handleConn: dropped conn\n")
		}
	}()
	if Verbose {
		fmt.Printf("handleConn: new req\n")
	}

	if _, ok := Running.Load("iqfeed"); !ok {
		if _, e := conn.Write([]byte("E,NO_DAEMON\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
		}
		return
	}
	if _, ok := Running.Load("admin"); !ok {
		if _, e := conn.Write([]byte("E,NO_ADMIN\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
		}
		return
	}

	// Start the clock
	deadline := time.Now().Add(deadlineCmd)

	if e := conn.SetDeadline(deadline); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		if _, e := conn.Write([]byte("E,CONN_SET_DEADLINE\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
		}
		return
	}

	upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
	if e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		if _, e := conn.Write([]byte("E,UPSTREAM CONN_TIMEOUT\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
		}
		return
	}
	defer upConn.Close()

	// Test if upstream conn is usable
	{
		if _, e := upConn.Write([]byte("TEST\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			if _, e := conn.Write([]byte("E,UPSTREAM_T\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}
		b := make([]byte, len("E,!SYNTAX_ERROR!,\r\n"))
		if _, e := upConn.Read(b); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			if _, e := conn.Write([]byte("E,UPSTREAM_T_RES\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}
		if !bytes.Equal(b, []byte("E,!SYNTAX_ERROR!,\r\n")) {
			fmt.Printf("handleConn: invalid res=%s\n", b)
			if _, e := conn.Write([]byte("E,UPSTREAM_T_INV\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}
	}

	if e := upConn.SetDeadline(deadline); e != nil {
		fmt.Printf("handleConn: %s\n", e.Error())
		if _, e := conn.Write([]byte("E,UPSTREAM SET_DEADLINE\r\n")); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
		}
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
		deadline = time.Now().Add(deadlineCmd)

		if e := conn.SetDeadline(deadline); e != nil {
			fmt.Printf("conn.SetDeadline e=%s\n", e.Error())
			if _, e := conn.Write([]byte("E,CONN_SET_DEADLINE\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}
		if e := upConn.SetDeadline(deadline); e != nil {
			fmt.Printf("upConn.SetDeadline e=%s\n", e.Error())
			if _, e := conn.Write([]byte("E,UPSTREAM SET_DEADLINE\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}

		// 1. client cmd
		bin, e := r.ReadBytes(byte('\n'))
		bin = bytes.TrimSpace(bin)
		if e != nil {
			fmt.Printf("conn.ReadBytes e=%s\n", e.Error())
			if _, e := conn.Write([]byte("E,CONN_READ_CMD\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
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
				fmt.Printf("QUIT-cmd\n")
			}
			return
		}

		if _, e := upConn.Write(bin); e != nil {
			fmt.Printf("upConn.Write e=%s\n", e.Error())
			if _, e := conn.Write([]byte("E,UPSTREAM_W\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}
		if _, e := upConn.Write([]byte("\r\n")); e != nil {
			fmt.Printf("upConn.Write e=%s\n", e.Error())
			if _, e := conn.Write([]byte("E,UPSTREAM_W\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}

		// 2. stream?
		if _, isStream := streamReplies[string(cmd)]; isStream {
			// give streaming some extra time
			stop := time.Now().Add(deadlineStream)
			if e := upConn.SetDeadline(stop); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
				if _, e := conn.Write([]byte("E,CONN_SET_DEADLINE\r\n")); e != nil {
					fmt.Printf("handleConn: %s\n", e.Error())
				}
				return
			}
			if e := conn.SetDeadline(stop); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
				if _, e := conn.Write([]byte("E,CONN_SET_DEADLINE\r\n")); e != nil {
					fmt.Printf("handleConn: %s\n", e.Error())
				}
				return
			}

			i := 0
			for ; i < loopLimit+1; i++ {
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
					fmt.Printf("upConn.Read e=%s\n", e.Error())
					if _, e := conn.Write([]byte("UPSTREAM_R\r\n")); e != nil {
						fmt.Printf("handleConn: %s\n", e.Error())
					}
					return
				}
				if _, e := conn.Write(bin); e != nil {
					fmt.Printf("conn.Write e=%s\n", e.Error())
					return
				}
				if _, e := conn.Write([]byte("\r\n")); e != nil {
					fmt.Printf("conn.Write e=%s\n", e.Error())
					return
				}
				if bytes.Equal(bin, []byte(EOM)) {
					// Done!
					if Verbose {
						fmt.Printf("End-Of-Stream\n")
					}
					break
				}
			}
			if i == loopLimit {
				fmt.Printf("CRIT: loopLimit(%d) reached, something wrong in code?\n", loopLimit)
				return
			}
		} else {
			// One reply
			bin, e := r2.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				fmt.Printf("line<< %s\n", bin)
			}

			if e != nil {
				fmt.Printf("upConn.Read e=%s\n", e.Error())
				if _, e := conn.Write([]byte("UPSTREAM_R\r\n")); e != nil {
					fmt.Printf("handleConn: %s\n", e.Error())
				}
				return
			}
			if _, e := conn.Write(bin); e != nil {
				fmt.Printf("conn.Write e=%s\n", e.Error())
				return
			}
			if _, e := conn.Write([]byte("\r\n")); e != nil {
				fmt.Printf("conn.Write e=%s\n", e.Error())
				return
			}
		}
	}
}
