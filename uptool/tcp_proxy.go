package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/maurice2k/tcpserver"
	"log/slog"
	"time"
)

/** maxDatapoints is the maximum of data we allow in non-chunked mode (else you get timeouts) */
const MaxDatapoints = 10000

/** defaultConnectTimeout is the default upstream.Connect timeout */
const defaultConnectTimeout = 3 * time.Second

/** EOM is End Of Message stream */
const EOM = "!ENDMSG!,"

/** streamReplies are all cmds we expect more than 1 result (till EOM) */
var streamReplies map[string]struct{}

/* deadlineCmd is the time a reply for a simple req>reply gets */
var deadlineCmd = 5 * time.Second

/* deadlineStream is the time a reply for a bigger reply gets */
// var deadlineStream = 15 * time.Second

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
func proxy(cmd []byte, lineLimit int, cb LineFunc) error {
	if _, ok := Running.Load("iqfeed"); !ok {
		return fmt.Errorf("iqfeed not running")
	}
	if _, ok := Running.Load("admin"); !ok {
		return fmt.Errorf("admin not ready")
	}

	conn, e := GetConn()
	if e != nil {
		return e
	}
	defer FreeConn(conn)

	if e := conn.IncreaseDeadline(deadlineCmd); e != nil {
		return fmt.Errorf("tcp_pool(GetConn) setDeadline e=" + e.Error())
	}

	if Verbose {
		slog.Info("tcp_proxy(proxy)", "stream", cmd)
	}
	if _, e := conn.WriteLine(cmd); e != nil {
		return e
	}

	i := 0
	for {
		// extend timeout with 5sec every line we receive
		if e := conn.IncreaseDeadline(deadlineCmd); e != nil {
			slog.Error("tcp_proxy(proxy) setDeadline", "e", e.Error())
			if Verbose {
				slog.Info("tcp_proxy(proxy)", "stream", "E,CONN_SET_DEADLINE")
			}
			return fmt.Errorf("E,CONN_SET_DEADLINE")
		}

		// read until EOM
		bin, e := conn.ReadLine()
		if Verbose {
			slog.Info("tcp_proxy(proxy)", "stream", bin)
		}
		if e != nil {
			return e
		}

		if tok := isError(bin); len(tok) > 0 {
			if Verbose {
				slog.Info("tcp_proxy(proxy) isError", "stream", bin, "tok", tok)
			}
			if len(tok) == 0 {
				return fmt.Errorf("%s", tok)
			}
			return fmt.Errorf("%s", tok[1])
		}

		if bytes.Equal(bin, []byte(EOM)) {
			// Done!
			if Verbose {
				slog.Info("tcp_proxy(proxy)", "End of stream")
			}
			break
		}

		i++
		if lineLimit != -1 && i >= lineLimit {
			// Stop
			return fmt.Errorf("CRIT: loopLimit(%d) reached, bin=%s\n", lineLimit, string(bin))
		}

		if e := cb(bin); e != nil {
			if Verbose {
				slog.Info("tcp_proxy(proxy) cbError", "stream", bin)
			}
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
			slog.Error("tcp_proxy close", "e", e.Error())
		}
		if Verbose {
			slog.Info("tcp_proxy dropped conn")
		}
	}()
	if Verbose {
		slog.Info("tcp_proxy new req")
	}

	r := bufio.NewReader(conn)
	w := bufio.NewWriterSize(conn, 1024*1024)
	defer w.Flush()

	for {
		// Start the clock
		deadline := time.Now().Add(deadlineCmd)
		if e := conn.SetDeadline(deadline); e != nil {
			slog.Error("tcp_proxy setDeadline", "e", e.Error())
			if _, e := w.Write([]byte("E,CONN_SET_DEADLINE\r\n")); e != nil {
				slog.Error("tcp_proxy WriteSetDeadline", "e", e.Error())
			}
			return
		}

		// 1. client cmd
		bin, e := r.ReadBytes(byte('\n'))
		if e != nil {
			slog.Error("tcp_proxy readBytes", "e", e.Error())
			if _, e := w.Write([]byte("E,CONN_READ_CMD\r\n")); e != nil {
				slog.Error("tcp_proxy writeConnReadCmd", "e", e.Error())
			}
			return
		}
		bin = bytes.TrimSpace(bin)
		if Verbose {
			slog.Info("tcp_proxy", "bin", bin)
		}

		// fake the responsive, we're already taking care of this
		if bytes.HasPrefix(bin, []byte("S,SET PROTOCOL")) {
			if !bytes.HasSuffix(bin, []byte("6.2")) {
				if Verbose {
					slog.Info("tcp_proxy", "e", "PROTOCOL_DEPRECATED_NEED_6.2")
				}
				if _, e := w.Write([]byte("E,PROTOCOL_DEPRECATED_NEED_6.2\r\n")); e != nil {
					slog.Error("tcp_proxy writeDeprecated", "e", e.Error())
				}
				return
			}

			if Verbose {
				slog.Info("tcp_proxy fakeCurrentProtocol")
			}
			if _, e := w.Write([]byte("S,CURRENT PROTOCOL,6.2\r\n")); e != nil {
				slog.Error("tcp_proxy writeCurrentProtocol", "e", e.Error())
			}
			if e := w.Flush(); e != nil {
				slog.Error("tcp_proxy FlushProtocol", "e", e.Error())
				return
			}

			continue
		}

		if e := proxy(bin, -1, func(line []byte) error {
			stop := time.Now().Add(deadlineCmd)
			if e := conn.SetDeadline(stop); e != nil {
				return fmt.Errorf("handleConn: conn.SetDeadline e=%s", e.Error())
			}

			if _, e := w.Write(line); e != nil {
				return fmt.Errorf("handleConn: conn.Write e=%s\n", e.Error())
			}
			if _, e := w.Write([]byte("\r\n")); e != nil {
				return fmt.Errorf("handleConn: conn.Write e=%s\n", e.Error())
			}
			return nil

		}); e != nil {
			slog.Error("tcp_proxy proxy", "e", e.Error())
			if _, e := w.Write([]byte("E," + e.Error() + "\r\n")); e != nil {
				slog.Error("tcp_proxy writeError", "e", e.Error())
			}
			return
		}

		// Flush once done
		if e := w.Flush(); e != nil {
			slog.Error("tcp_proxy FlushProxy", "e", e.Error())
			return
		}
	}
}
