// Package testtenant loads the test tenant: an invented person with a known
// invented password, a few chats, and recorded model turns that the fake
// provider plays. rebuild loads it, so a fresh checkout ends with someone
// who can sign in and a model that answers with no key and no network.
//
// The test tenant is told from a real one by a mark in its own database,
// written when the loader makes it. See IsTest and Find. The fixtures are
// in fixtures/test-tenant, and docs/architecture/test-tenant.md has the
// formats.
package testtenant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	fixture "github.com/jonathanmkosar/keystone/fixtures/test-tenant"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Fixtures is the test tenant as its fixture files describe it, read and
// checked.
type Fixtures struct {
	// Name is the invented person's name.
	Name string
	// Password is the invented password the person signs in with. It is
	// published in the repo and guards nothing.
	Password string
	// Scripts are the recorded model turns, for the fake provider.
	Scripts []fake.Script
	// Chats are the chats the loader makes, in order of file name.
	Chats []Chat
	// Cases are the use cases and their expected results.
	Cases []Case
}

// Chat is one chat to load. The fixture file holds the person's messages
// only. Replies holds what the recorded turns answer to each.
type Chat struct {
	// File is the fixture file's name, and what the loader remembers the
	// chat by.
	File    string
	Title   string
	Starred bool
	// Messages are the person's messages, in the order they were sent.
	Messages []ChatMessage
	// Replies holds the model's reply to each message, from the recorded
	// turns.
	Replies []seam.Message
	// Hash changes when the chat's file or any recorded turn changes.
	Hash string
}

// ChatMessage is one message the person sent: a question, or the results
// of the tools the reply before it asked for.
type ChatMessage struct {
	seam.Message
	// Edits is the number, counted from one, of an earlier message in the
	// file that this one replaces, as an edit does. The chat then has a
	// branch at that point, and this message is on the current one. Zero
	// means the message follows the reply before it.
	Edits int `json:"edits,omitempty"`
}

// Case is one use case: something done in the test tenant, and what must
// come of it.
type Case struct {
	Name string `json:"name"`
	// Kind is turn, search or chat.
	Kind string `json:"kind"`
	// Ask is the person's message, for a turn.
	Ask string `json:"ask,omitempty"`
	// Query is what is searched for, for a search.
	Query string `json:"query,omitempty"`
	// Title names the chat, for a chat.
	Title  string `json:"title,omitempty"`
	Expect Expect `json:"expect"`
}

// Expect is a use case's expected result. A field that is not set is not
// checked.
type Expect struct {
	// StopReason is why the reply ended, for a turn.
	StopReason string `json:"stop_reason,omitempty"`
	// TextContains lists pieces of text the reply holds, for a turn, or
	// that some message on the chat's current branch holds, for a chat.
	TextContains []string `json:"text_contains,omitempty"`
	// ToolCalls lists the names of the tools the reply asks for, in order.
	ToolCalls []string `json:"tool_calls,omitempty"`
	// Titles lists the chats a search finds, in any order, and no others.
	Titles []string `json:"titles,omitempty"`
	// Messages is how many messages the chat's current branch has.
	Messages int `json:"messages,omitempty"`
	// Starred is whether the chat has its star.
	Starred *bool `json:"starred,omitempty"`
	// Branches is the most messages that share one parent on the current
	// branch: 2 where a message was edited once.
	Branches int `json:"branches,omitempty"`
}

// Shipped returns the fixtures compiled into the binary from
// fixtures/test-tenant.
func Shipped() (Fixtures, error) {
	return Read(fixture.FS)
}

// Read reads and checks the fixtures in a folder laid out as
// fixtures/test-tenant is. It refuses a file that is not marked as test
// data, a field it does not know, a chat the recorded turns cannot answer,
// and a use case it cannot run.
func Read(fsys fs.FS) (Fixtures, error) {
	var fx Fixtures

	var person struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if _, err := readFile(fsys, "tenant.json", &person); err != nil {
		return fx, err
	}
	fx.Name, fx.Password = strings.TrimSpace(person.Name), person.Password
	if fx.Name == "" || fx.Password == "" {
		return fx, errors.New("tenant.json needs a name and a password")
	}

	turns, err := fs.Sub(fsys, "turns")
	if err != nil {
		return fx, err
	}
	if fx.Scripts, err = fake.Load(turns); err != nil {
		return fx, fmt.Errorf("turns: %w", err)
	}
	p, err := fake.New(fx.Scripts, fake.Options{})
	if err != nil {
		return fx, fmt.Errorf("turns: %w", err)
	}
	turnsHash, err := hashFolder(turns)
	if err != nil {
		return fx, err
	}

	names, err := jsonFiles(fsys, "chats")
	if err != nil {
		return fx, err
	}
	titles := map[string]bool{}
	for _, name := range names {
		var file struct {
			Title    string        `json:"title"`
			Starred  bool          `json:"starred"`
			Messages []ChatMessage `json:"messages"`
		}
		raw, err := readFile(fsys, "chats/"+name, &file)
		if err != nil {
			return fx, err
		}
		c := Chat{File: name, Title: strings.TrimSpace(file.Title), Starred: file.Starred, Messages: file.Messages}
		if c.Title == "" {
			return fx, fmt.Errorf("chats/%s has no title", name)
		}
		if titles[c.Title] {
			return fx, fmt.Errorf("chats/%s: two chats are titled %q", name, c.Title)
		}
		titles[c.Title] = true
		if c.Replies, err = replay(p, c.Messages); err != nil {
			return fx, fmt.Errorf("chats/%s: %w", name, err)
		}
		sum := sha256.Sum256(append(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), turnsHash...))
		c.Hash = hex.EncodeToString(sum[:])
		fx.Chats = append(fx.Chats, c)
	}

	names, err = jsonFiles(fsys, "cases")
	if err != nil {
		return fx, err
	}
	seen := map[string]bool{}
	for _, name := range names {
		var file struct {
			Cases []Case `json:"cases"`
		}
		if _, err := readFile(fsys, "cases/"+name, &file); err != nil {
			return fx, err
		}
		if len(file.Cases) == 0 {
			return fx, fmt.Errorf("cases/%s holds no use cases", name)
		}
		for _, c := range file.Cases {
			if err := checkCase(c); err != nil {
				return fx, fmt.Errorf("cases/%s: %w", name, err)
			}
			if seen[c.Name] {
				return fx, fmt.Errorf("cases/%s: two use cases are named %q", name, c.Name)
			}
			seen[c.Name] = true
			fx.Cases = append(fx.Cases, c)
		}
	}
	return fx, nil
}

// Provider returns the fake provider with the test tenant's recorded turns.
// It is the only model the test tenant is given.
func Provider(fx Fixtures, o fake.Options) (*fake.Provider, error) {
	return fake.New(fx.Scripts, o)
}

// replay plays a chat's messages through the recorded turns and returns the
// reply to each. Nothing is stored: it is how a chat's fixture is checked
// before any database is touched.
func replay(p seam.Provider, msgs []ChatMessage) ([]seam.Message, error) {
	if len(msgs) == 0 {
		return nil, errors.New("the chat has no messages")
	}
	replies := make([]seam.Message, len(msgs))
	// before[i] is the branch a message was added to, and after[i] is that
	// branch with the message and its reply.
	before := make([][]seam.Message, len(msgs))
	after := make([][]seam.Message, len(msgs))
	var stop seam.StopReason
	for i, m := range msgs {
		if m.Role != seam.RoleUser {
			return nil, fmt.Errorf("message %d is not the person's: the replies come from the recorded turns", i+1)
		}
		if err := seam.ValidateMessage(m.Message); err != nil {
			return nil, fmt.Errorf("message %d: %w", i+1, err)
		}
		switch {
		case m.Edits < 0 || m.Edits > i:
			return nil, fmt.Errorf("message %d edits message %d, which is not an earlier message", i+1, m.Edits)
		case m.Edits > 0:
			before[i] = before[m.Edits-1]
		case i > 0:
			before[i] = after[i-1]
		}
		req := seam.Request{Messages: append(append([]seam.Message(nil), before[i]...), m.Message)}
		resp, err := seam.Call(context.Background(), p, req)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i+1, err)
		}
		replies[i] = resp.Message
		after[i] = append(req.Messages, resp.Message)
		stop = resp.StopReason
	}
	if stop == seam.StopToolCall {
		return nil, errors.New("the chat ends on a tool call: add the tool's result as the next message")
	}
	return replies, nil
}

// readFile decodes one fixture file into v and returns its bytes. Every
// fixture file is a JSON object with "test_data": true beside its own
// fields.
func readFile(fsys fs.FS, name string, v any) ([]byte, error) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if string(bytes.TrimSpace(fields["test_data"])) != "true" {
		return nil, fmt.Errorf(`%s is not marked as test data: it needs "test_data": true`, name)
	}
	delete(fields, "test_data")
	rest, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(rest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%s holds more than one JSON value", name)
	}
	return raw, nil
}

// jsonFiles lists the .json files at the top of a folder, by name.
func jsonFiles(fsys fs.FS, dir string) ([]string, error) {
	paths, err := fs.Glob(fsys, dir+"/*.json")
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s holds no fixture files", dir)
	}
	sort.Strings(paths)
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = strings.TrimPrefix(p, dir+"/")
	}
	return names, nil
}

// hashFolder returns one hash for the .json files at the top of a folder.
// Line endings are left out, so a Windows checkout and a Linux one agree.
func hashFolder(fsys fs.FS) ([]byte, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(h, "%s\n", name)
		h.Write(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
	}
	return h.Sum(nil), nil
}
