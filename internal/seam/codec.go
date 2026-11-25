package seam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// The Encode functions validate a value and write it as JSON. The Decode
// functions read JSON, refuse a field they do not know, and validate the
// value. The chat record, the audit log, the API's event stream and the fake
// provider's fixtures all hold the format in this form.

func EncodeMessage(m Message) ([]byte, error)   { return encode(m, ValidateMessage) }
func EncodeRequest(r Request) ([]byte, error)   { return encode(r, ValidateRequest) }
func EncodeEvent(e Event) ([]byte, error)       { return encode(e, ValidateEvent) }
func EncodeResponse(r Response) ([]byte, error) { return encode(r, ValidateResponse) }

func DecodeMessage(raw []byte) (Message, error)   { return decode(raw, ValidateMessage) }
func DecodeRequest(raw []byte) (Request, error)   { return decode(raw, ValidateRequest) }
func DecodeEvent(raw []byte) (Event, error)       { return decode(raw, ValidateEvent) }
func DecodeResponse(raw []byte) (Response, error) { return decode(raw, ValidateResponse) }

func encode[T any](v T, validate func(T) error) ([]byte, error) {
	if err := validate(v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func decode[T any](raw []byte, validate func(T) error) (T, error) {
	var v, zero T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return zero, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return zero, fmt.Errorf("more than one JSON value")
	}
	if err := validate(v); err != nil {
		return zero, err
	}
	return v, nil
}
