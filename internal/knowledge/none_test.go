package knowledge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/knowledgetest"
)

func openNone(t *testing.T) knowledge.Store {
	t.Helper()
	s, err := knowledge.Open(context.Background(), knowledge.None, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The none adapter passes the contract every adapter must pass. It cannot be
// given content, so the harness has no Seed.
func TestNonePassesTheContract(t *testing.T) {
	knowledgetest.Run(t, knowledgetest.Harness{New: openNone})
}

// What a caller sees with no store: nothing to recall, nothing to read, and
// a plain "not supported" where a fact would have been saved. None of it is
// a failure a chat has to handle differently from an empty store.
func TestNoneBehavesAsAnEmptyStore(t *testing.T) {
	ctx := context.Background()
	s := openNone(t)

	if caps := s.Capabilities(); caps != (knowledge.Capabilities{}) {
		t.Errorf("none claims %+v", caps)
	}
	if entries, err := s.Catalog(ctx); err != nil || len(entries) != 0 {
		t.Errorf("Catalog: %v, %v", entries, err)
	}
	if hits, err := s.Search(ctx, knowledge.Query{Text: "anything at all"}); err != nil || len(hits) != 0 {
		t.Errorf("Search: %v, %v", hits, err)
	}
	if _, err := s.ReadPage(ctx, "1"); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("ReadPage: %v", err)
	}
	if _, err := s.ReadSource(ctx, "1"); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("ReadSource: %v", err)
	}
	n := knowledge.NewSource{Space: "notes", Name: "chat-facts", Facts: []knowledge.Fact{{Text: "An invented fact."}}}
	if _, err := s.AddSource(ctx, n); !errors.Is(err, knowledge.ErrNotSupported) {
		t.Errorf("AddSource: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
