package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/maurice2k/tcpserver"
)

/** maxDatapoints is the maximum of data we allow in non-chunked mode (else you get timeouts) */
const MaxDatapoints = 10000

/** defaultConnectTimeout is the default upstream.Connect timeout */
const defaultConnectTimeout = 3 * time.Second

/** EOM is End Of Message stream */
const EOM = "!ENDMSG!,"

// Pre-allocated byte slices to avoid allocations in hot paths
var (
	eomBytes  = []byte(EOM)
	crlfBytes = []byte("\r\n")
	errPrefix = []byte("E,")

	// Static error responses
	errConnSetDeadline = []byte("E,CONN_SET_DEADLINE\r\n")
	errConnReadCmd     = []byte("E,CONN_READ_CMD\r\n")
	errProtocolOld     = []byte("E,PROTOCOL_DEPRECATED_NEED_6.2\r\n")
	respProtocol62     = []byte("S,CURRENT PROTOCOL,6.2\r\n")
)

// Buffer pools to reduce allocations per connection
var (
	readerPool = sync.Pool{New: func() any { return bufio.NewReaderSize(nil, 4*1024) }}
	writerPool = sync.Pool{New: func() any { return bufio.NewWriterSize(nil, 64*1024) }}
)

// ProtocolError represents an IQFeed protocol error (not a connection error)
type ProtocolError struct {
	Msg string
}

func (e *ProtocolError) Error() string {
	return e.Msg
}

/** streamReplies are all cmds we expect more than 1 result (till EOM) */
var streamReplies map[string]struct{}

/* deadlineCmd is the time a reply for a simple req>reply gets */
var deadlineCmd = 17 * time.Second

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
	if bytes.HasPrefix(bin, errPrefix) {
		// Error
		// i.e. "E,!NO_DATA!,,", "E,Unauthorized user ID.,"
		return bytes.SplitN(bin, []byte(","), 4)
	}
	return nil
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

	var protocolErr error
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
			return fmt.Errorf("ReadLine e=%s", e.Error())
		}

		if bytes.Equal(bin, eomBytes) {
			// Forward EOM to client, then done
			if Verbose {
				slog.Info("tcp_proxy(proxy) End of stream")
			}
			if e := cb(bin); e != nil {
				return e
			}
			break
		}

		if tok := isError(bin); len(tok) > 0 {
			if Verbose {
				slog.Info("tcp_proxy(proxy) isError", "stream", bin, "tok", tok)
			}
			// Forward error line to client, then store error and continue to EOM
			if e := cb(bin); e != nil {
				return e
			}
			if len(tok) < 2 {
				protocolErr = &ProtocolError{Msg: string(bin)}
			} else {
				protocolErr = &ProtocolError{Msg: string(tok[1])}
			}
			continue
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

	return protocolErr
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

	r := readerPool.Get().(*bufio.Reader)
	r.Reset(conn)
	defer readerPool.Put(r)

	w := writerPool.Get().(*bufio.Writer)
	w.Reset(conn)
	defer func() {
		w.Flush()
		writerPool.Put(w)
	}()

	for {
		// Start the clock
		deadline := time.Now().Add(deadlineCmd)
		if e := conn.SetDeadline(deadline); e != nil {
			slog.Error("tcp_proxy setDeadline", "e", e.Error())
			if _, e := w.Write(errConnSetDeadline); e != nil {
				slog.Error("tcp_proxy WriteSetDeadline", "e", e.Error())
			}
			return
		}

		// 1. client cmd
		bin, e := r.ReadBytes(byte('\n'))
		if e != nil {
			slog.Error("tcp_proxy readBytes", "e", e.Error())
			if _, e := w.Write(errConnReadCmd); e != nil {
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
				if _, e := w.Write(errProtocolOld); e != nil {
					slog.Error("tcp_proxy writeDeprecated", "e", e.Error())
				}
				return
			}

			if Verbose {
				slog.Info("tcp_proxy fakeCurrentProtocol")
			}
			if _, e := w.Write(respProtocol62); e != nil {
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
			if _, e := w.Write(crlfBytes); e != nil {
				return fmt.Errorf("handleConn: conn.Write e=%s\n", e.Error())
			}
			return nil

		}); e != nil {
			if _, ok := e.(*ProtocolError); ok {
				// Protocol errors (like !NO_DATA!) are already forwarded to client
				// Don't close connection - client may send more requests
				if Verbose {
					slog.Info("tcp_proxy proxy protocol error", "e", e.Error())
				}
			} else {
				// Connection error - close and exit
				slog.Error("tcp_proxy proxy", "e", e.Error())
				return
			}
		}

		// Flush once done
		if e := w.Flush(); e != nil {
			slog.Error("tcp_proxy FlushProxy", "e", e.Error())
			return
		}
	}
}
