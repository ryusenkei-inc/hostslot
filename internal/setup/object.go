package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// object is a JSON object that keeps the order of its keys, so a settings
// file is rewritten without reordering what jobgate does not touch.
type object []member

type member struct {
	key   string
	value json.RawMessage
}

func parseObject(data []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var o object
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected token %v", tok)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		o = append(o, member{key, value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// object returns the member key as an object, or an empty one when absent.
func (o object) object(key string) (object, error) {
	raw, ok := o.get(key)
	if !ok {
		return object{}, nil
	}
	inner, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return inner, nil
}

func (o *object) set(key string, value json.RawMessage) {
	for i, m := range *o {
		if m.key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key, value})
}

func (o *object) delete(key string) {
	for i, m := range *o {
		if m.key == key {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}

func (o object) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(m.key)
		b.Write(key)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}
