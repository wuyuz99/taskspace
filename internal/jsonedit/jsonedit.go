// Package jsonedit changes JSON documents a person arranged without
// rearranging them: what is not changed is written back as the bytes it was
// read as, and keys keep their order.
package jsonedit

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Object is a JSON object that keeps its keys in order and its values as the
// bytes they were read as.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// Parse reads one JSON object.
func Parse(b []byte) (*Object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	o := &Object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.Set(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("unexpected data after the object")
	}
	return o, nil
}

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Get returns key's value as it was read or set.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Set replaces key's value, or adds key at the end.
func (o *Object) Set(key string, v json.RawMessage) {
	if o.vals == nil {
		o.vals = map[string]json.RawMessage{}
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// Del removes key.
func (o *Object) Del(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Marshal writes the object with its values as stored; keys are plain
// strings, written with the same escaping-free encoder as everything else.
func (o *Object) Marshal() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := MarshalPlain(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// MarshalPlain is json.Marshal without HTML escaping: a command such as
// `make lint && echo ok > log` must be written as typed, not as &&.
func MarshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// MarshalList joins raw elements into a JSON array without re-encoding them.
func MarshalList(items []json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, it); err != nil {
			return nil, err
		}
		b.Write(compact.Bytes())
	}
	b.WriteByte(']')
	return b.Bytes(), nil
}

// IsNull says raw is JSON null.
func IsNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// Same reports whether two documents hold the same value, so that a change
// with nothing to do leaves the file, and its formatting, untouched.
func Same(a, b []byte) (bool, error) {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false, err
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb), nil
}
