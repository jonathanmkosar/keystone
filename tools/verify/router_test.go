package main

import "testing"

// The router's case of the sensitive content check finds nothing wrong
// with the router as built.
func TestRouterProblems(t *testing.T) {
	for _, p := range routerProblems() {
		t.Error(p)
	}
}
