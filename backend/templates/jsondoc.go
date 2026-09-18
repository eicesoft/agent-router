package templates

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// doc is a JSON object that remembers key order, so a merged config keeps the
// user's original field ordering instead of the alphabetical order a plain
// map would produce. Values decode recursively: nested objects become *doc,
// numbers stay json.Number so they reprint unchanged.
type doc struct {
	keys []string
	vals map[string]any
}

func newDoc() *doc { return &doc{vals: map[string]any{}} }

func (d *doc) set(key string, value any) {
	if _, ok := d.vals[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.vals[key] = value
}

func (d *doc) delete(key string) {
	if _, ok := d.vals[key]; !ok {
		return
	}
	delete(d.vals, key)
	for i, k := range d.keys {
		if k == key {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

// object returns the object at key, keeping its position when present,
// creating an empty one when absent or replacing a non-object value.
func (d *doc) object(key string) *doc {
	if child, ok := d.vals[key].(*doc); ok {
		return child
	}
	child := newDoc()
	d.set(key, child)
	return child
}

func (d *doc) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range d.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valueJSON, err := json.Marshal(d.vals[key])
		if err != nil {
			return nil, err
		}
		buf.Write(valueJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// parseDoc decodes a JSON object preserving key order; ok is false when data
// is not valid JSON or the top level is not an object. JSONC extras (line and
// block comments, trailing commas) are stripped first so .jsonc configs such
// as kilo.jsonc merge instead of falling through to the fresh-document path.
func parseDoc(data []byte) (*doc, bool) {
	data = stripJSONC(data)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := decodeValue(dec)
	if err != nil {
		return nil, false
	}
	d, ok := value.(*doc)
	return d, ok
}

// stripJSONC removes // and /* */ comments and trailing commas before closing
// braces/brackets, leaving strings (and their contents) untouched. Comments go
// first: a comma can be followed by a comment before the closing brace, e.g.
// "0.5, // cap\n}", so the comma only becomes recognizable as trailing after
// the comment is gone.
func stripJSONC(data []byte) []byte {
	return stripTrailingCommas(stripComments(data))
}

// stripComments removes // line comments and /* */ block comments, preserving
// string literals verbatim.
func stripComments(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out.WriteByte('\n') // keep line structure
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			i++
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

// stripTrailingCommas drops commas that are directly followed by nothing but
// whitespace before a closing brace or bracket.
func stripTrailingCommas(data []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out.WriteByte(c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\n' || data[j] == '\r') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue // trailing comma: drop
			}
		}
		out.WriteByte(c)
	}
	return out.Bytes()
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch delim, _ := tok.(json.Delim); delim {
	case '{':
		d := newDoc()
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected object key %v", keyTok)
			}
			value, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			d.set(key, value)
		}
		if _, err := dec.Token(); err != nil { // closing brace
			return nil, err
		}
		return d, nil
	case '[':
		items := []any{}
		for dec.More() {
			value, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		if _, err := dec.Token(); err != nil { // closing bracket
			return nil, err
		}
		return items, nil
	default:
		return tok, nil
	}
}
