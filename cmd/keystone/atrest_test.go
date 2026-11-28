package main

import (
	"strings"
	"testing"
)

// The server seals a plain person when it starts (P15-06), before it serves
// them: testServer makes the person plain, and the client's test signs them
// in after, on the sealed folder.
func TestServeSealsAPlainPersonAtStart(t *testing.T) {
	_, id, log := testServer(t)
	if !strings.Contains(log.String(), "tenant "+id+": sealed") {
		t.Fatalf("the log does not say the person was sealed:\n%s", log)
	}
	if strings.Contains(log.String(), "Test Person One") {
		t.Error("the log names the person")
	}
}
