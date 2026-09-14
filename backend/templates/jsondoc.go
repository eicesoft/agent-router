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
// is not valid JSON or the top level is not an object.
func parseDoc(data []byte) (*doc, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := decodeValue(dec)
	if err != nil {
		return nil, false
	}
	d, ok := value.(*doc)
	return d, ok
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
