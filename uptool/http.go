package main

import (
	"bytes"
	"encoding/json"
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
	Volume   string
}

type SearchLine struct {
	Ticker      string
	MarketId    string
	Description string
	Type        string
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

func search(w http.ResponseWriter, r *http.Request) {
	// Construct cmd
	var cmd []byte
	{
		// SBF,[Field To Search],[Search String],[Filter Type],[Filter Value],[RequestID]<CR><LF>
		// sprintf("SBF,%s,%s,%s,%s", $field, $search, "t", implode(" ", array_keys($securityTypes)));
		keys := []string{
			"field",
			"search",
			"type",
		}

		args := make(map[string]string)
		for _, key := range keys {
			val := r.URL.Query().Get(key)
			if val == "" {
				w.WriteHeader(400)
				if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[" + key + "] missing"}); e != nil {
					fmt.Printf("HTTP[search] e=%s\n", e.Error())
				}
				return
			}

			if key == "field" {
				if val == "SYMBOL" {
					val = "s"
				} else if val == "DESCRIPTION" {
					val = "d"
				} else {
					w.WriteHeader(400)
					if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[" + key + "] invalid, can only search on SYMBOL|DESCRIPTION"}); e != nil {
						fmt.Printf("HTTP[search] e=%s\n", e.Error())
					}
					return
				}
			}

			if key == "type" {
				if val == "EQUITY" {
					val = "1"
				} else {
					w.WriteHeader(400)
					if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[" + key + "] invalid, can only have EQUITY"}); e != nil {
						fmt.Printf("HTTP[search] e=%s\n", e.Error())
					}
					return
				}
			}

			args[key] = val
		}

		cmd = []byte(fmt.Sprintf("SBF,%s,%s,%s,%s", args["field"], args["search"], "t", args["type"]))
	}

	// Parse lines
	var out []SearchLine
	if e := proxy(cmd, -1, func(bin []byte) error {
		buf := bytes.SplitN(bin, []byte(","), 9)
		if len(buf) < 6 {
			return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
		}

		// LS,TSLA,21,1,TESLA  INC.,
		out = append(out, SearchLine{
			Ticker:      string(buf[1]),
			MarketId:    string(buf[2]),
			Description: string(buf[4]),
			Type:        string(buf[3]),
		})
		return nil

	}); e != nil {
		fmt.Printf("HTTP[search] e=%s\n", e.Error())
		w.WriteHeader(400)
		if e := writer.Err(w, r, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
			fmt.Printf("HTTP[search] e=%s\n", e.Error())
		}
		return
	}

	if e := writer.Encode(w, r, out); e != nil {
		fmt.Printf("buf.Flush e=%s\n", e.Error())
	}
}

func data(w http.ResponseWriter, r *http.Request) {
	// Collect args to construct cmd
	var (
		cmd  []byte
		dp   int
		mode string
	)
	{
		asset := r.URL.Query().Get("asset")
		if asset == "" {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[asset] missing"}); e != nil {
				fmt.Printf("HTTP[data] e=%s\n", e.Error())
			}
			return
		}
		rangeStr := r.URL.Query().Get("range")
		if rangeStr == "" {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[range] missing"}); e != nil {
				fmt.Printf("HTTP[data] e=%s\n", e.Error())
			}
			return
		}
		dpStr := r.URL.Query().Get("datapoints")
		if dpStr == "" {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[datapoints] missing"}); e != nil {
				fmt.Printf("HTTP[data] e=%s\n", e.Error())
			}
			return
		}
		var e error
		dp, e = strconv.Atoi(dpStr)
		if e != nil {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[datapoints] not a number"}); e != nil {
				fmt.Printf("HTTP[data] e=%s\n", e.Error())
			}
			return
		}

		mode = r.URL.Query().Get("mode")
		if rangeStr == "DAILY" {
			cmd = []byte(fmt.Sprintf("HDX,%s,%d", asset, dp))
		} else if rangeStr == "WEEKLY" {
			cmd = []byte(fmt.Sprintf("HWX,%s,%d", asset, dp))
		} else if rangeStr == "MONTHLY" {
			cmd = []byte(fmt.Sprintf("HMX,%s,%d", asset, dp))
		} else {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[range] not valid, possible=DAILY|WEEKLY|MONTHLY"}); e != nil {
				fmt.Printf("HTTP[data] e=%s\n", e.Error())
			}
			return
		}
	}

	if mode == "chunked" {
		w.Header().Set("Content-Type", "application/json")
		ohlc := &OHLC{}
		enc := json.NewEncoder(w)

		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "Could not get Flusher-instance"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}

		i := 0
		if e := proxy(cmd, -1, func(bin []byte) error {
			buf := bytes.SplitN(bin, []byte(","), 9)
			if len(buf) < 7 {
				return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
			}

			// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
			ohlc.Datetime = string(buf[1])
			ohlc.High = string(buf[2])
			ohlc.Low = string(buf[3])
			ohlc.Open = string(buf[4])
			ohlc.Close = string(buf[5])
			ohlc.Volume = string(buf[6])

			if e := enc.Encode(ohlc); e != nil {
				return e
			}

			i++
			if (i % 100 == 0) {
				flusher.Flush()
			}
			return nil

		}); e != nil {
			fmt.Printf("HTTP[data] e=%s\n", e.Error())
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
				fmt.Printf("HTTP[data] e=%s\n", e.Error())
			}
			return
		}

		flusher.Flush()
		return
	}

	// Parse lines
	out := make([]OHLC, 0, dp)
	if e := proxy(cmd, dp+100, func(bin []byte) error {
		buf := bytes.SplitN(bin, []byte(","), 9)
		if len(buf) < 7 {
			return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
		}

		// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
		out = append(out, OHLC{
			Datetime: string(buf[1]),
			High:     string(buf[2]),
			Low:      string(buf[3]),
			Open:     string(buf[4]),
			Close:    string(buf[5]),
			Volume:   string(buf[6]),
		})
		return nil

	}); e != nil {
		fmt.Printf("HTTP[data] e=%s\n", e.Error())
		w.WriteHeader(400)
		if e := writer.Err(w, r, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
			fmt.Printf("HTTP[data] e=%s\n", e.Error())
		}
		return
	}

	if e := writer.Encode(w, r, out); e != nil {
		fmt.Printf("buf.Flush e=%s\n", e.Error())
	}
}

func intervals(w http.ResponseWriter, r *http.Request) {
	// Collect args to construct cmd
	var (
		cmd      []byte
		interval int
		dp       int
		mode     string
	)
	{
		asset := r.URL.Query().Get("asset")
		if asset == "" {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[asset] missing"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}
		intervalStr := r.URL.Query().Get("interval")
		if intervalStr == "" {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[interval] missing"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}
		var e error
		interval, e = strconv.Atoi(intervalStr)
		if e != nil {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[interval] not a number"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}
		// TODO: Something fancy here to validate the interval?

		dpStr := r.URL.Query().Get("datapoints")
		if dpStr == "" {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[datapoints] missing"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}
		dp, e = strconv.Atoi(dpStr)
		if e != nil {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "GET[datapoints] not a number"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}

		mode = r.URL.Query().Get("mode")
		cmd = []byte(fmt.Sprintf("HIX,%s,%d,%d", asset, interval, dp))
	}

	if mode == "chunked" {
		w.Header().Set("Content-Type", "application/json")
		ohlc := &OHLC{}
		enc := json.NewEncoder(w)

		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(400)
			if e := writer.Err(w, r, writer.ErrorRes{Error: "Could not get Flusher-instance"}); e != nil {
				fmt.Printf("HTTP[intervals] e=%s\n", e.Error())
			}
			return
		}

		i := 0
		if e := proxy(cmd, -1, func(bin []byte) error {
			buf := bytes.SplitN(bin, []byte(","), 9)
			if len(buf) < 7 {
				return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
			}

			// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
			ohlc.Datetime = string(buf[1])
			ohlc.High = string(buf[2])
			ohlc.Low = string(buf[3])
			ohlc.Open = string(buf[4])
			ohlc.Close = string(buf[5])
			ohlc.Volume = string(buf[6])

			if e := enc.Encode(ohlc); e != nil {
				return e
			}

			i++
			if (i % 100 == 0) {
				flusher.Flush()
			}
			return nil

		}); e != nil {
			fmt.Printf("HTTP[data] e=%s\n", e.Error())
			// devnote: cannot print error as it might crash in-between
			return
		}

		flusher.Flush()
		return
	}

	// Parse lines
	out := make([]OHLC, 0, dp)
	if e := proxy(cmd, dp+100, func(bin []byte) error {
		buf := bytes.SplitN(bin, []byte(","), 9)
		if len(buf) < 7 {
			return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
		}

		// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
		out = append(out, OHLC{
			Datetime: string(buf[1]),
			High:     string(buf[2]),
			Low:      string(buf[3]),
			Open:     string(buf[4]),
			Close:    string(buf[5]),
			Volume:   string(buf[6]),
		})
		return nil

	}); e != nil {
		fmt.Printf("HTTP[data] e=%s\n", e.Error())
		w.WriteHeader(400)
		if e := writer.Err(w, r, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
			fmt.Printf("HTTP[data] e=%s\n", e.Error())
		}
		return
	}

	if e := writer.Encode(w, r, out); e != nil {
		fmt.Printf("buf.Flush e=%s\n", e.Error())
	}
}

func httpListen(addr string) {
	// HTTP server
	mux.Title = "IQ API"
	mux.Desc = "IQConnect HTTP abstraction"
	mux.Add("/", doc, "This documentation")
	mux.Add("/verbose", verbose, "Toggle verbosity-mode")

	mux.Add("/ohlc", data, "Read OHLC ?asset=AAPL&range=DAILY|WEEKLY|MONTHLY&datapoints=10")
	mux.Add("/ohlc-intervals", intervals, "Read OHLC (interval in seconds) ?asset=AAPL&interval=100&datapoints=10")
	mux.Add("/search", search, "Search assets ?field=SYMBOL|DESCRIPTION&search=*&type=EQUITY")

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
