package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// message is one JSON-RPC 2.0 message: a request (an id and a method), a
// notification (a method and no id), or a response (an id and a result or
// an error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// isResponse reports whether a message answers a request.
func (m message) isResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// idKey is a message's id as this client wrote it: the requests it sends
// have whole numbers for ids. A server that echoes one as a string is
// matched too.
func idKey(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 1 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if _, err := strconv.ParseInt(s, 10, 64); err == nil {
				return s
			}
			return "s:" + s
		}
	}
	return string(raw)
}

// decode reads one line or body from a server: one message, or a batch of
// them as an array, which servers of 2025-03-26 may send.
func decode(raw []byte) ([]message, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '[' {
		var batch []message
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, errors.New("the server sent something that is not JSON-RPC")
		}
		return batch, nil
	}
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errors.New("the server sent something that is not JSON-RPC")
	}
	return []message{m}, nil
}

// errTooLarge is a message longer than Options.MaxMessageBytes.
type errTooLarge struct{ max int64 }

func (e errTooLarge) Error() string {
	return fmt.Sprintf("the server sent a message larger than %d bytes, which is more than Keystone reads", e.max)
}

// readLine reads one line, without its line ending, of at most max bytes.
func readLine(r *bufio.Reader, max int64) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if int64(len(line)+len(chunk)) > max {
			return nil, errTooLarge{max}
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return bytes.TrimRight(line, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case len(line) > 0 && errors.Is(err, io.EOF):
			return bytes.TrimRight(line, "\r\n"), nil
		default:
			return nil, err
		}
	}
}
