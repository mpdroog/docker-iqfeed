package writer

import (
	"encoding/json"
	"net/http"
	"strings"

	prettyjson "github.com/hokaccha/go-prettyjson"
	"github.com/vmihailenco/msgpack"
)

type Encoder interface {
	Encode(interface{}) error
}

type PrettyJSONEncoder struct {
	r     *http.Request
	w     http.ResponseWriter
	first bool
}

func (p *PrettyJSONEncoder) Encode(data interface{}) error {
	isCurl := strings.Contains(p.r.Header.Get("User-Agent"), "curl/")
	if isCurl {
		// Coloured output for CLI
		s, e := prettyjson.Marshal(data)
		if e != nil {
			return e
		}
		if p.first {
			p.first = false
			p.w.Header().Set("Content-Type", "application/json")
		}
		p.w.Write(s)
		return nil
	}

	// JSON idented
	s, e := json.MarshalIndent(data, "", "  ")
	if e != nil {
		return e
	}
	if p.first {
		p.first = false
		p.w.Header().Set("Content-Type", "application/json")
	}
	p.w.Write(s)
	p.w.Write([]byte("\r\n"))
	return nil
}

// Encode function
func Encode(w http.ResponseWriter, r *http.Request, data interface{}) error {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		// Machine, dense output
		enc := json.NewEncoder(w)
		w.Header().Set("Content-Type", "application/json")
		if e := enc.Encode(data); e != nil {
			return e
		}
		return nil
	}
	if strings.Contains(accept, "application/x-msgpack") {
		s, e := msgpack.Marshal(data)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/x-msgpack")
		w.Write(s)
		return nil
	}

	isCurl := strings.Contains(r.Header.Get("User-Agent"), "curl/")
	if isCurl {
		// Coloured output for CLI
		s, e := prettyjson.Marshal(data)
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(s)
		return nil
	}

	// JSON idented
	s, e := json.MarshalIndent(data, "", "  ")
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(s)
	w.Write([]byte("\r\n"))
	return nil
}

func ChunkedEncoder(w http.ResponseWriter, r *http.Request) Encoder {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w)
	}
	if strings.Contains(accept, "application/x-msgpack") {
		w.Header().Set("Content-Type", "application/x-msgpack")
		return msgpack.NewEncoder(w)
	}

	// default, content-type set by encoder
	return &PrettyJSONEncoder{w: w, r: r}
}

// ErrorRes struct
type ErrorRes struct {
	Error  string
	Detail interface{}
}

// Err return a error based on the ErroRes struct format
func Err(w http.ResponseWriter, r *http.Request, m ErrorRes) error {
	return Encode(w, r, &m)
}
