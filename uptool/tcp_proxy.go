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
var loopLimit = 10000

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
		"SBF": struct{}{},
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
func proxy(cmd []byte, cb LineFunc) error {
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

	if _, e := upConn.Write(cmd); e != nil {
		return e
	}
	if _, e := upConn.Write([]byte("\r\n")); e != nil {
		return e
	}

	// TODO: Refactor search into streaming JSON..?

	lineLimit := loopLimit
	// Allow search to get more lines
	if bytes.HasPrefix(cmd, []byte("SBF,")) {
		lineLimit = -1 // No limit for search
	}
	for i := 0; ; i++ {
		if lineLimit != -1 && i >= lineLimit {
			// Stop
			return fmt.Errorf("CRIT: loopLimit(%d) reached, something wrong in code?\n", lineLimit)
		}

		// give streaming some extra time
		stop := time.Now().Add(deadlineStream)
		if e := upConn.SetDeadline(stop); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			return fmt.Errorf("E,CONN_SET_DEADLINE")
		}

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

	for {
		// Start the clock
		deadline := time.Now().Add(deadlineCmd)
		if e := conn.SetDeadline(deadline); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			if _, e := conn.Write([]byte("E,CONN_SET_DEADLINE\r\n")); e != nil {
				fmt.Printf("handleConn: %s\n", e.Error())
			}
			return
		}

		r := bufio.NewReader(conn)
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

		if e := proxy(bin, func(line []byte) error {
			stop := time.Now().Add(deadlineCmd)
			if e := conn.SetDeadline(stop); e != nil {
				return fmt.Errorf("conn.SetDeadline e=%s", e.Error())
			}

			if _, e := conn.Write(line); e != nil {
				return fmt.Errorf("conn.Write e=%s\n", e.Error())
			}
			if _, e := conn.Write([]byte("\r\n")); e != nil {
				return fmt.Errorf("conn.Write e=%s\n", e.Error())
			}
			return nil

		}); e != nil {
			fmt.Printf("handleConn: %s\n", e.Error())
			return
		}
	}
}
