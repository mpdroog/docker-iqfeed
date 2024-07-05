package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/itshosted/webutils/muxdoc"
	"github.com/mpdroog/docker-iqfeed/iqapi/writer"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
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

func chunkedStream(w http.ResponseWriter, r *http.Request, cmd []byte, csvHeader []byte) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "Could not get Flusher-instance"}); e != nil {
			slog.Error("HTTP[intervals] getFlusher", "e", e.Error())
		}
		return
	}

	// buffer 1MB
	ww := bufio.NewWriterSize(w, 1024*1024)
	defer ww.Flush()

	i := 0
	if e := proxy(cmd, -1, func(bin []byte) error {
		if i == 0 {
			if _, e := ww.Write(csvHeader); e != nil {
				return e
			}
			if _, e := ww.Write([]byte("\r\n")); e != nil {
				return e
			}
		}
		if _, e := ww.Write(bin); e != nil {
			return e
		}
		if _, e := ww.Write([]byte("\r\n")); e != nil {
			return e
		}

		i++
		return nil

	}); e != nil {
		slog.Error("HTTP[intervals] proxy", "e", e.Error())
		if i == 0 {
			if e := writer.Err(w, r, 404, writer.ErrorRes{Error: "Read failure", Detail: e.Error()}); e != nil {
				slog.Error("HTTP[intervals] proxy.Write", "e", e.Error())
			}
		}
		return
	}

	if i == 0 {
		// Nothing sent to client
		if e := writer.Err(w, r, 404, writer.ErrorRes{Error: "No data"}); e != nil {
			slog.Error("HTTP[intervals] proxy.WriteNodata", "e", e.Error())
		}
	}

	flusher.Flush()
}

// Return API Documentation (paths)
func doc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(404)
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
	slog.Info("HTTP[verbose]", "set", Verbose)

	w.Header().Set("Content-Type", "application/json")
	if _, e := w.Write([]byte(msg)); e != nil {
		slog.Error("HTTP[verbose] Write", "e", e.Error())
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
				if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[" + key + "] missing"}); e != nil {
					slog.Error("HTTP[search] WriteMissing", "e", e.Error())
				}
				return
			}

			if key == "field" {
				if val == "SYMBOL" {
					val = "s"
				} else if val == "DESCRIPTION" {
					val = "d"
				} else {
					if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[" + key + "] invalid, can only search on SYMBOL|DESCRIPTION"}); e != nil {
						slog.Error("HTTP[search] WriteInvalidField", "e", e.Error())
					}
					return
				}
			}

			if key == "type" {
				if val == "EQUITY" {
					val = "1"
				} else {
					if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[" + key + "] invalid, can only have EQUITY"}); e != nil {
						slog.Error("HTTP[search] WriteInvalidType", "e", e.Error())
					}
					return
				}
			}

			args[key] = val
		}

		cmd = []byte(fmt.Sprintf("SBF,%s,%s,%s,%s", args["field"], args["search"], "t", args["type"]))
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "Could not get Flusher-instance"}); e != nil {
			slog.Error("HTTP[search] getFlusher", "e", e.Error())
		}
		return
	}
	enc := writer.ChunkedEncoder(w, r)

	i := 0
	// Parse lines
	var line SearchLine
	if e := proxy(cmd, -1, func(bin []byte) error {
		csv, ok := enc.(writer.StringEncoder)
		if ok {
			if i == 0 {
				if e := csv.Write([]string{"MessageID", "Symbol", "ListedMarketID", "SecurityTypeID", "Name", ""}); e != nil {
					return e
				}
			}
			// TODO: use bytes.SplitN and typecast?
			buf := strings.SplitN(string(bin), ",", 6)
			if e := csv.Write(buf); e != nil {
				return e
			}

			i++
			if i%100 == 0 {
				enc.(writer.FlushEncoder).Flush()
				flusher.Flush()
			}
			return nil
		}

		buf := bytes.SplitN(bin, []byte(","), 6)
		if len(buf) < 6 {
			return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
		}

		// LS,TSLA,21,1,TESLA  INC.,
		line.Ticker = string(buf[1])
		line.MarketId = string(buf[2])
		line.Description = string(buf[4])
		line.Type = string(buf[3])

		if e := enc.Encode(line); e != nil {
			return e
		}
		if _, e := w.Write([]byte("\r\n")); e != nil {
			return e
		}

		i++
		if i%100 == 0 {
			flusher.Flush()
		}
		return nil

	}); e != nil {
		slog.Error("HTTP[search] proxy", "e", e.Error())
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
			slog.Error("HTTP[search] WriteUpstreamError", "e", e.Error())
		}
		return
	}

	if i == 0 {
		// Nothing sent to client
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "No data"}); e != nil {
			slog.Error("HTTP[search] WriteNoData", "e", e.Error())
		}
		return
	}

	if fenc, ok := enc.(writer.FlushEncoder); ok {
		fenc.Flush()
	}
	flusher.Flush()
	return
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
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[asset] missing"}); e != nil {
				slog.Error("HTTP[data] WriteAssetMissing", "e", e.Error())
			}
			return
		}
		rangeStr := r.URL.Query().Get("range")
		if rangeStr == "" {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[range] missing"}); e != nil {
				slog.Error("HTTP[data] WriteRangeMissing", "e", e.Error())
			}
			return
		}
		dpStr := r.URL.Query().Get("datapoints")
		if dpStr == "" {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[datapoints] missing"}); e != nil {
				slog.Error("HTTP[data] WriteDatapointsMissing", "e", e.Error())
			}
			return
		}
		var e error
		dp, e = strconv.Atoi(dpStr)
		if e != nil {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[datapoints] not a number"}); e != nil {
				slog.Error("HTTP[data] WriteDtatapointNaN", "e", e.Error())
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
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[range] not valid, possible=DAILY|WEEKLY|MONTHLY"}); e != nil {
				slog.Error("HTTP[data] WriteInvalidRange", "e", e.Error())
			}
			return
		}
	}

	if mode == "chunked" {
		chunkedStream(w, r, cmd, []byte("MessageID, DateStamp, High, Low, Open, Close, PeriodVolume, OpenInterest,"))
		return
	}

	if dp+100 > MaxDatapoints {
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "MAX_DATAPOINTS", Detail: fmt.Sprintf("rejecting more than %d datapoints, please set mode=chunked", MaxDatapoints)}); e != nil {
			slog.Error("HTTP[data] WriteMaxDatapoints", "e", e.Error())
		}
		return
	}

	// Parse lines
	out := make([]OHLC, 0, dp)
	i := 0
	if e := proxy(cmd, dp+100, func(bin []byte) error {
		buf := bytes.SplitN(bin, []byte(","), 9)
		if len(buf) < 7 {
			return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
		}

		// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
		i++
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
		slog.Error("HTTP[data] proxy", "e", e.Error())
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
			slog.Error("HTTP[data] WriteUpstreamError", "e", e.Error())
		}
		return
	}

	if i == 0 {
		// Nothing sent to client
		if e := writer.Err(w, r, 404, writer.ErrorRes{Error: "No data"}); e != nil {
			slog.Error("HTTP[data] WriteNoData", "e", e.Error())
		}
		return
	}

	if e := writer.Encode(w, r, 200, out); e != nil {
		slog.Error("HTTP[data] WriteEncode", "e", e.Error())
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
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[asset] missing"}); e != nil {
				slog.Error("HTTP[intervals] WriteAssetMissing", "e", e.Error())
			}
			return
		}
		intervalStr := r.URL.Query().Get("interval")
		if intervalStr == "" {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[interval] missing"}); e != nil {
				slog.Error("HTTP[intervals] WriteIntervalMissing", "e", e.Error())
			}
			return
		}
		var e error
		interval, e = strconv.Atoi(intervalStr)
		if e != nil {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[interval] not a number"}); e != nil {
				slog.Error("HTTP[intervals] WriteIntervalNaN", "e", e.Error())
			}
			return
		}
		// TODO: Something fancy here to validate the interval?

		dpStr := r.URL.Query().Get("datapoints")
		if dpStr == "" {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[datapoints] missing"}); e != nil {
				slog.Error("HTTP[intervals] WriteDtapointsMissing", "e", e.Error())
			}
			return
		}
		dp, e = strconv.Atoi(dpStr)
		if e != nil {
			if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "GET[datapoints] not a number"}); e != nil {
				slog.Error("HTTP[intervals] WriteDtapointsMissing", "e", e.Error())
			}
			return
		}

		mode = r.URL.Query().Get("mode")
		cmd = []byte(fmt.Sprintf("HIX,%s,%d,%d", asset, interval, dp))
	}

	if mode == "chunked" {
		chunkedStream(w, r, cmd, []byte("MessageID, TimeStamp, High, Low, Open, Close, TotalVolume, PeriodVolume, NumberofTrades,"))
		return
	}

	if dp+100 > MaxDatapoints {
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "MAX_DATAPOINTS", Detail: fmt.Sprintf("rejecting more than %d datapoints, please set mode=chunked", MaxDatapoints)}); e != nil {
			slog.Error("HTTP[intervals] WriteMaxDatapoints", "e", e.Error())
		}
		return
	}

	// Parse lines
	i := 0
	out := make([]OHLC, 0, dp)
	if e := proxy(cmd, dp+100, func(bin []byte) error {
		buf := bytes.SplitN(bin, []byte(","), 9)
		if len(buf) < 7 {
			return fmt.Errorf("WARN: Failed parsing line=%s\n", bin)
		}

		// LH,2023-05-25,288.8400,272.8500,287.9100,280.9900,878367,0,
		i++
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
		slog.Error("HTTP[intervals] proxy", "e", e.Error())
		if e := writer.Err(w, r, 400, writer.ErrorRes{Error: "Upstream error", Detail: e.Error()}); e != nil {
			slog.Error("HTTP[intervals] WriteUpstreamError", "e", e.Error())
		}
		return
	}

	if i == 0 {
		// Nothing sent to client
		if e := writer.Err(w, r, 404, writer.ErrorRes{Error: "No data"}); e != nil {
			slog.Error("HTTP[intervals] WriteNoData", "e", e.Error())
		}
		return
	}

	if e := writer.Encode(w, r, 200, out); e != nil {
		slog.Error("HTTP[intervals] WriteEncode", "e", e.Error())
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

	// pprof
	mux.Add("/debug/pprof/", pprof.Index, "performance-profiler")
	mux.Add("/debug/pprof/cmdline", pprof.Cmdline, "Cmdline responds with the running program's command line")
	mux.Add("/debug/pprof/profile", pprof.Profile, "Profile responds with the pprof-formatted cpu profile.")
	mux.Add("/debug/pprof/symbol", pprof.Symbol, "Symbol looks up the program counters listed in the request, responding with a table mapping program counters to function names.")
	mux.Add("/debug/pprof/trace", pprof.Trace, "Trace responds with the execution trace in binary form.")

	var e error
	server := &http.Server{
		Addr:        addr,
		TLSConfig:   nil,
		Handler:     mux.Mux,
		ReadTimeout: 5 * time.Second,
		// WriteTimeout: 10 * time.Second,
		IdleTimeout: 20 * time.Second,
	}

	ln, e = net.Listen("tcp", server.Addr)
	if e != nil {
		panic(e)
	}

	if e := server.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)}); e != nil {
		panic(e)
	}
}
