package fake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Script is one scripted exchange: which requests it answers, and what the
// model sends back at each step.
type Script struct {
	// Name says what the script is for. No two scripts share a name.
	Name string `json:"name"`
	// When says which requests the script answers.
	When When `json:"when"`
	// Steps are the model's replies, in order. The first answers the
	// person's message. Each later one answers the tool results that came
	// back for the step before it.
	Steps []Step `json:"steps"`
}

// When is the conditions a request must meet. Every condition that is set
// must hold. A When with nothing set answers any request.
//
// The text conditions ignore letter case and the spaces around the text.
type When struct {
	// UserText is the whole text of the last message the person wrote.
	UserText string `json:"user_text,omitempty"`
	// UserTextContains is a part of that text.
	UserTextContains string `json:"user_text_contains,omitempty"`
	// SystemContains is a part of the request's system text.
	SystemContains string `json:"system_contains,omitempty"`
	// Model is the request's model name, exactly.
	Model string `json:"model,omitempty"`
}

// Step is what the model does on one call. It is one of three things:
//
//	a reply                  the whole reply is streamed
//	an error                 the call fails before anything is sent
//	a reply and a cut_after  the reply breaks off part of the way through
type Step struct {
	// Reply is a whole reply in the seam's format. A reply with no model
	// takes the request's.
	Reply *seam.Response `json:"reply,omitempty"`
	// Error is the failure the call ends with. With cut_after and no
	// error, the failure is the one a dropped connection gives.
	Error *seam.Error `json:"error,omitempty"`
	// CutAfter is how many characters of the reply's text, thinking and
	// tool input are sent before the stream fails.
	CutAfter *int `json:"cut_after,omitempty"`
}

// Decode reads one script file. It refuses a file that is not marked as
// test data, a field it does not know, and any script that is not valid.
func Decode(raw []byte) ([]Script, error) {
	var file struct {
		TestData bool     `json:"test_data"`
		Scripts  []Script `json:"scripts"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}
	if !file.TestData {
		return nil, errors.New(`the file is not marked as test data: it needs "test_data": true`)
	}
	if len(file.Scripts) == 0 {
		return nil, errors.New("the file holds no scripts")
	}
	scripts, err := prepare(file.Scripts)
	if err != nil {
		return nil, err
	}
	if err := checkSet(scripts); err != nil {
		return nil, err
	}
	return scripts, nil
}

// Load reads every .json file at the top of fsys as a script file, in order
// of file name, and checks the scripts as one set.
func Load(fsys fs.FS) ([]Script, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("the folder holds no script files")
	}
	sort.Strings(names)
	var all []Script
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		scripts, err := Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		all = append(all, scripts...)
	}
	if err := checkSet(all); err != nil {
		return nil, err
	}
	return all, nil
}

// prepare checks every script and returns a copy with each tool call's
// input written the short way, so the same script always streams the same
// characters however its file is laid out.
func prepare(scripts []Script) ([]Script, error) {
	out := make([]Script, len(scripts))
	for i, s := range scripts {
		if strings.TrimSpace(s.Name) == "" {
			return nil, fmt.Errorf("script %d has no name", i+1)
		}
		if len(s.Steps) == 0 {
			return nil, fmt.Errorf("script %q has no steps", s.Name)
		}
		steps := make([]Step, len(s.Steps))
		for j, st := range s.Steps {
			step, err := prepareStep(st)
			if err != nil {
				return nil, fmt.Errorf("script %q: step %d: %w", s.Name, j+1, err)
			}
			// The step after this one is reached only when tool results
			// come back, so this one must ask for a tool and not fail.
			if j < len(s.Steps)-1 && (step.Reply == nil || step.CutAfter != nil || step.Reply.StopReason != seam.StopToolCall) {
				return nil, fmt.Errorf("script %q: step %d can never be reached, because step %d does not stop for a tool call", s.Name, j+2, j+1)
			}
			steps[j] = step
		}
		s.Steps = steps
		out[i] = s
	}
	return out, nil
}

func prepareStep(st Step) (Step, error) {
	if st.Error != nil {
		// The seam checks an error as part of its error event.
		if err := seam.ValidateEvent(seam.Event{Type: seam.EventError, Error: st.Error}); err != nil {
			return Step{}, fmt.Errorf("error: %w", err)
		}
		failure := *st.Error
		st.Error = &failure
	}
	if st.Reply == nil {
		if st.Error == nil {
			return Step{}, errors.New("it needs a reply or an error")
		}
		if st.CutAfter != nil {
			return Step{}, errors.New("cut_after needs a reply to cut")
		}
		return st, nil
	}
	if err := seam.ValidateResponse(*st.Reply); err != nil {
		return Step{}, fmt.Errorf("reply: %w", err)
	}
	reply := *st.Reply
	reply.Message.Blocks = nil
	size := 0
	for _, b := range st.Reply.Message.Blocks {
		if b.Type == seam.BlockToolCall {
			var short bytes.Buffer
			if err := json.Compact(&short, b.Input); err != nil {
				return Step{}, fmt.Errorf("reply: tool call %s: %w", b.ID, err)
			}
			b.Input = short.Bytes()
		}
		size += utf8.RuneCountInString(b.Text) + utf8.RuneCountInString(b.Signature) + utf8.RuneCount(b.Input)
		reply.Message.Blocks = append(reply.Message.Blocks, b)
	}
	st.Reply = &reply
	if st.CutAfter == nil {
		if st.Error != nil {
			return Step{}, errors.New("it has a reply and an error: add cut_after to say how much of the reply is sent before the error")
		}
		return st, nil
	}
	n := *st.CutAfter
	if n < 0 || n >= size {
		return Step{}, fmt.Errorf("cut_after is %d, and the reply sends %d characters: the cut must be at least 0 and less than that", n, size)
	}
	st.CutAfter = &n
	return st, nil
}

// checkSet refuses two scripts with one name, and two that answer exactly
// the same requests, because the second could never be chosen.
func checkSet(scripts []Script) error {
	names := map[string]bool{}
	whens := map[When]string{}
	for _, s := range scripts {
		if names[s.Name] {
			return fmt.Errorf("two scripts are named %q", s.Name)
		}
		names[s.Name] = true
		w := When{
			UserText:         fold(s.When.UserText),
			UserTextContains: fold(s.When.UserTextContains),
			SystemContains:   fold(s.When.SystemContains),
			Model:            s.When.Model,
		}
		if other, dup := whens[w]; dup {
			return fmt.Errorf("scripts %q and %q answer the same requests, so the second can never be chosen", other, s.Name)
		}
		whens[w] = s.Name
	}
	return nil
}

// fold puts text in the form the conditions compare: lower case, with no
// space around it.
func fold(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
