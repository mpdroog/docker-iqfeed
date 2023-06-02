package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/itshosted/webutils/muxdoc"
	"github.com/mpdroog/docker-iqfeed/iqapi/writer"
	"net"
	"net/http"
	"strconv"
	"time"
)

var (
	mux muxdoc.MuxDoc
	ln  net.Listener
)

// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
type OHLC struct {
	Datetime string
	High     string
	Low      string
	Open     string
	Close    string
}

// Return API Documentation (paths)
func doc(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(404)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(mux.String()))
}

func verbose(w http.ResponseWriter, r *http.Request) {
	msg := `{"success": true, "msg": "Set verbosity to `
	if Verbose {
		Verbose = false
		msg += "OFF"
	} else {
		Verbose = true
		msg += "ON"
	}
	msg += `"}`
	fmt.Printf("HTTP.Verbosity set to %t\n", Verbose)

	w.Header().Set("Content-Type", "application/json")
	if _, e := w.Write([]byte(msg)); e != nil {
		fmt.Printf("verbose: " + e.Error())
		return
	}
}

func data(w http.ResponseWriter, r *http.Request) {
	// Collect args to construct cmd
	var cmd string
	{
		asset := r.URL.Query().Get("asset")
		if asset == "" {
			w.WriteHeader(400)
			writer.Err(w, r, writer.ErrorRes{Error: "GET[asset] missing"})
			return
		}
		rangeStr := r.URL.Query().Get("range")
		if rangeStr == "" {
			w.WriteHeader(400)
			writer.Err(w, r, writer.ErrorRes{Error: "GET[range] missing"})
			return
		}
		dpStr := r.URL.Query().Get("datapoints")
		if dpStr == "" {
			w.WriteHeader(400)
			writer.Err(w, r, writer.ErrorRes{Error: "GET[datapoints] missing"})
			return
		}
		dp, e := strconv.Atoi(dpStr)
		if e != nil {
			w.WriteHeader(400)
			writer.Err(w, r, writer.ErrorRes{Error: "GET[datapoints] not a number"})
			return
		}

		if rangeStr == "DAILY" {
			cmd = fmt.Sprintf("HDX,%s,%d", asset, dp)
		} else if rangeStr == "WEEKLY" {
			cmd = fmt.Sprintf("HWX,%s,%d", asset, dp)
		} else if rangeStr == "MONTHLY" {
			cmd = fmt.Sprintf("HMX,%s,%d", asset, dp)
		} else {
			w.WriteHeader(400)
			writer.Err(w, r, writer.ErrorRes{Error: "GET[range] not valid, possible=DAILY|WEEKLY|MONTHLY"})
			return
		}
	}

	// Collect data
	{
		// Start the clock
		dur, e := time.ParseDuration("10s")
		if e != nil {
			fmt.Printf("data: %s\n", e.Error())
			w.WriteHeader(500)
			writer.Err(w, r, writer.ErrorRes{Error: "Failed parsing duration"})
			return
		}
		upConn, e := net.DialTimeout("tcp", "127.0.0.1:9100", defaultConnectTimeout)
		if e != nil {
			fmt.Printf("data: %s\n", e.Error())
			w.WriteHeader(500)
			writer.Err(w, r, writer.ErrorRes{Error: "Failed connecting to TCP 9100"})
			return
		}
		defer upConn.Close()

		deadline := time.Now().Add(dur)
		if e := upConn.SetDeadline(deadline); e != nil {
			fmt.Printf("data: %s\n", e.Error())
			w.WriteHeader(500)
			writer.Err(w, r, writer.ErrorRes{Error: "Failed configuring deadline"})
			return
		}

		if _, e := upConn.Write([]byte("S,SET PROTOCOL,6.2\r\n")); e != nil {
			fmt.Printf("[upConn write] %s\n", e.Error())
			w.WriteHeader(500)
			writer.Err(w, r, writer.ErrorRes{Error: "Failed sending protocol-cmd"})
			return
		}

		rUp := bufio.NewReader(upConn)
		// S,CURRENT PROTOCOL,6.2
		{
			bin, e := rUp.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				fmt.Printf("stream<< %s\n", bin)
			}
			if e != nil {
				fmt.Printf("[upConn Read] %s\n", e.Error())
				w.WriteHeader(500)
				writer.Err(w, r, writer.ErrorRes{Error: "Failed reading protocol-cmd"})
				return
			}
		}

		if _, e := upConn.Write([]byte(cmd)); e != nil {
			fmt.Printf("data: %s\n", e.Error())
			w.WriteHeader(500)
			writer.Err(w, r, writer.ErrorRes{Error: "Failed sending ohlc-cmd"})
			return
		}
		if _, e := upConn.Write([]byte("\r\n")); e != nil {
			fmt.Printf("data: %s\n", e.Error())
			w.WriteHeader(500)
			writer.Err(w, r, writer.ErrorRes{Error: "Failed sending ohlc-EOL"})
			return
		}

		var out []OHLC
		for {
			// read until EOM
			bin, e := rUp.ReadBytes(byte('\n'))
			bin = bytes.TrimSpace(bin)
			if Verbose {
				fmt.Printf("stream<< %s\n", bin)
			}
			if e != nil {
				fmt.Printf("upConn.Read e=%s\n", e.Error())
				w.WriteHeader(500)
				writer.Err(w, r, writer.ErrorRes{Error: "Failed reading upstream"})
				return
			}

			if bytes.HasPrefix(bin, []byte("E,")) {
				// Error
				// E,!NO_DATA!,,", "E,Unauthorized user ID.,
				buf := bytes.SplitN(bin, []byte(","), 4)

				w.WriteHeader(400)
				writer.Err(w, r, writer.ErrorRes{Error: "Upstream error", Detail: buf})
				return
			}

			if bytes.Equal(bin, []byte(EOM)) {
				// Done!
				if Verbose {
					fmt.Printf("End-Of-Stream\n")
				}
				break
			}

			// Parse line
			buf := bytes.SplitN(bin, []byte(","), 9)

			// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
			out = append(out, OHLC{
				Datetime: string(buf[1]),
				High:     string(buf[2]),
				Low:      string(buf[3]),
				Open:     string(buf[4]),
				Close:    string(buf[5]),
			})
		}

		if e := writer.Encode(w, r, out); e != nil {
			fmt.Printf("buf.Flush e=%s\n", e.Error())
		}
	}
}

func httpListen(addr string) {
	// HTTP server
	mux.Title = "IQ API"
	mux.Desc = "IQConnect HTTP abstraction"
	mux.Add("/", doc, "This documentation")
	mux.Add("/verbose", verbose, "Toggle verbosity-mode")

	mux.Add("/ohlc", data, "Read OHLC ?asset=AAPL&range=DAILY|WEEKLY|MONTHLY&datapoints=10")

	var e error
	server := &http.Server{
		Addr:         addr,
		TLSConfig:    nil,
		Handler:      mux.Mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	ln, e = net.Listen("tcp", server.Addr)
	if e != nil {
		panic(e)
	}

	if e := server.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)}); e != nil {
		panic(e)
	}
}
