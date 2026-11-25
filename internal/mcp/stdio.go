package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Stdio reaches a server by running a command and speaking to it over its
// standard input and output, one JSON message to a line.
//
// The command is run directly, never through a shell, with exactly the
// environment in Env. What the command writes to its standard error is kept,
// its last part only, and named in the error when the command ends.
type Stdio struct {
	// Command is the program to run: a path, or a name found on PATH.
	Command string
	// Args are its arguments, each passed as it is.
	Args []string
	// Env is the whole environment the command gets, each "NAME=value".
	// Nil gives it an empty environment, not this process's.
	Env []string
	// Dir is the folder it runs in. Empty means this process's.
	Dir string
	// Redact, when not nil, is applied to the command's standard error
	// before it is put in an error, so a token the command repeats is not.
	Redact func(string) string
}

func (Stdio) kind() string { return "stdio" }

// How long a command is given to stop after its input is closed, and to
// let go of its output after it stops.
const (
	stopGrace = 2 * time.Second
	pipeGrace = 2 * time.Second
	tailBytes = 2048
)

type stdioWire struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	writes sync.Mutex
	in     inbound
	max    int64
	stderr *tail
	redact func(string) string

	mu      sync.Mutex
	waiting map[string]chan message

	done chan struct{} // closed when the command's output has ended
	err  error         // why it ended, set before done is closed
}

func (s Stdio) open(o Options, in inbound) (wire, error) {
	if strings.TrimSpace(s.Command) == "" {
		return nil, errors.New("no command was given to run")
	}
	cmd := exec.Command(s.Command, s.Args...)
	cmd.Env = s.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Dir = s.Dir
	// A command that leaves its output open in a process of its own after
	// it stops is not waited for past this.
	cmd.WaitDelay = pipeGrace
	hide(cmd)
	w := &stdioWire{cmd: cmd, in: in, max: o.MaxMessageBytes, stderr: &tail{}, redact: s.Redact, waiting: map[string]chan message{}, done: make(chan struct{})}
	cmd.Stderr = w.stderr
	out, outW := io.Pipe()
	cmd.Stdout = outW
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	w.stdin = stdin
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("the command could not be started: %w", unwrapExec(err))
	}
	go func() {
		err := cmd.Wait()
		outW.CloseWithError(exited{err})
	}()
	go w.read(out)
	return w, nil
}

// exited is the end of a command's output: the command stopped.
type exited struct{ err error }

func (e exited) Error() string { return "the command stopped" }

func unwrapExec(err error) error {
	var ee *exec.Error
	if errors.As(err, &ee) {
		return ee.Err
	}
	return err
}

// read reads the command's output until it ends, and hands each message on.
func (w *stdioWire) read(out *io.PipeReader) {
	r := bufio.NewReaderSize(out, 64<<10)
	var end error
	for {
		line, err := readLine(r, w.max)
		if err != nil {
			end = err
			break
		}
		msgs, err := decode(line)
		if err != nil {
			continue // a line that is not a message, such as a banner: let go
		}
		for _, m := range msgs {
			w.take(m)
		}
	}
	out.CloseWithError(end) // a command that sends too much is not read further
	w.mu.Lock()
	w.err = w.why(end)
	w.mu.Unlock()
	close(w.done)
	if _, ok := end.(errTooLarge); ok && w.cmd.Process != nil {
		w.cmd.Process.Kill()
	}
}

// why says why the command's output ended, for an error.
func (w *stdioWire) why(end error) error {
	var x exited
	var tl errTooLarge
	var msg string
	switch {
	case errors.As(end, &tl):
		return tl
	case errors.As(end, &x) && x.err != nil:
		msg = "the connector's command stopped: " + x.err.Error()
	default:
		msg = "the connector's command stopped"
	}
	if t := strings.TrimSpace(w.stderr.String()); t != "" {
		if w.redact != nil {
			t = w.redact(t)
		}
		msg += ". It said: " + clip(strings.Join(strings.Fields(t), " "), 600)
	}
	return fmt.Errorf("%w: %s", ErrClosed, msg)
}

// take hands one message from the server on: a response to the request
// waiting for it, and anything else to the client, whose reply is sent.
func (w *stdioWire) take(m message) {
	if m.isResponse() {
		w.mu.Lock()
		ch, ok := w.waiting[idKey(m.ID)]
		delete(w.waiting, idKey(m.ID))
		w.mu.Unlock()
		if ok {
			ch <- m
		}
		return
	}
	if reply := w.in(m); reply != nil {
		if raw, err := jsonLine(*reply); err == nil {
			w.write(raw)
		}
	}
}

func (w *stdioWire) write(line []byte) error {
	w.writes.Lock()
	defer w.writes.Unlock()
	_, err := w.stdin.Write(line)
	return err
}

func (w *stdioWire) roundTrip(ctx context.Context, id string, payload []byte) (message, error) {
	ch := make(chan message, 1)
	w.mu.Lock()
	select {
	case <-w.done:
		err := w.err
		w.mu.Unlock()
		return message{}, err
	default:
	}
	w.waiting[id] = ch
	w.mu.Unlock()
	forget := func() {
		w.mu.Lock()
		delete(w.waiting, id)
		w.mu.Unlock()
	}
	if err := w.write(append(payload, '\n')); err != nil {
		forget()
		select {
		case <-w.done:
			return message{}, w.err
		case <-time.After(100 * time.Millisecond):
			return message{}, fmt.Errorf("%w: the command's input could not be written", ErrClosed)
		}
	}
	select {
	case m := <-ch:
		return m, nil
	case <-w.done:
		forget()
		return message{}, w.err
	case <-ctx.Done():
		forget()
		return message{}, ctx.Err()
	}
}

func (w *stdioWire) notify(ctx context.Context, payload []byte) error {
	select {
	case <-w.done:
		return w.err
	default:
	}
	return w.write(append(payload, '\n'))
}

func (w *stdioWire) negotiated(string) {}

// close closes the command's input, which tells a server to stop, waits a
// little for it to, and then stops it.
func (w *stdioWire) close() error {
	w.writes.Lock()
	w.stdin.Close()
	w.writes.Unlock()
	select {
	case <-w.done:
		return nil
	case <-time.After(stopGrace):
	}
	if w.cmd.Process != nil {
		w.cmd.Process.Kill()
	}
	select {
	case <-w.done:
	case <-time.After(stopGrace + pipeGrace):
	}
	return nil
}

func jsonLine(m message) ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// tail keeps the last part of what a command writes to its standard error.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBytes {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailBytes:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
