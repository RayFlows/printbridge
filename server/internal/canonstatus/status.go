// Package canonstatus translates Canon IVEC back-channel reports into CUPS reasons.
package canonstatus

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

const maxFrame = 64 << 10

type State uint8

const (
	Unknown State = iota
	Ready
	PaperEmpty
)

// Decoder accepts a byte stream with arbitrary transport boundaries and framing noise.
// Only complete, successful status reports may change an existing device reason.
type Decoder struct{ buffer []byte }

func (d *Decoder) Feed(data []byte, emit func(State)) {
	for len(data) > 0 {
		n := min(len(data), 4096)
		d.buffer = append(d.buffer, data[:n]...)
		data = data[n:]
		for {
			start := bytes.Index(d.buffer, []byte("<cmd"))
			if start < 0 {
				d.keepSuffix()
				break
			}
			d.buffer = d.buffer[start:]
			if len(d.buffer) < 5 {
				break
			}
			if !strings.ContainsRune(" \t\r\n>", rune(d.buffer[4])) {
				d.buffer = d.buffer[4:]
				continue
			}
			end := bytes.Index(d.buffer, []byte("</cmd>"))
			// A fresh root recovers from an abandoned or malformed earlier report.
			next := bytes.Index(d.buffer[4:], []byte("<cmd"))
			if next >= 0 && (end < 0 || next+4 < end) {
				d.buffer = d.buffer[next+4:]
				continue
			}
			if end < 0 {
				if len(d.buffer) > maxFrame {
					d.keepSuffix()
				}
				break
			}
			end += len("</cmd>")
			if end <= maxFrame {
				if state := parse(d.buffer[:end]); state != Unknown {
					emit(state)
				}
			}
			d.buffer = d.buffer[end:]
		}
	}
}

func (d *Decoder) keepSuffix() {
	if len(d.buffer) > 4 {
		copy(d.buffer, d.buffer[len(d.buffer)-4:])
		d.buffer = d.buffer[:4]
	}
}

func parse(frame []byte) State {
	decoder := xml.NewDecoder(bytes.NewReader(frame))
	values := make(map[string]string)
	var path []string
	var field, text string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Unknown
		}
		switch element := token.(type) {
		case xml.StartElement:
			if field != "" || len(path) >= 16 {
				return Unknown
			}
			path = append(path, element.Name.Local)
			switch element.Name.Local {
			case "operation", "response", "status", "status_detail":
				if _, duplicate := values[element.Name.Local]; duplicate {
					return Unknown
				}
				field, text = element.Name.Local, ""
			}
		case xml.CharData:
			if field != "" {
				text += string(element)
				if len(text) > 256 {
					return Unknown
				}
			}
		case xml.EndElement:
			if element.Name.Local == field {
				values[field] = strings.TrimSpace(text)
				field, text = "", ""
			}
			path = path[:len(path)-1]
		case xml.Directive:
			return Unknown
		}
	}
	if values["operation"] != "GetStatusResponse" || values["response"] != "OK" {
		return Unknown
	}
	if values["status"] == "stopped" && values["status_detail"] == "MediaEmpty" {
		return PaperEmpty
	}
	detail, hasDetail := values["status_detail"]
	if hasDetail && detail == "" && (values["status"] == "idle" || values["status"] == "processing") {
		return Ready
	}
	return Unknown
}
