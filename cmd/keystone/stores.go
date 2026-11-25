package main

// The knowledge store adapters this binary can open. Each registers itself
// by name when it is imported, and the knowledge.adapter setting picks one.
import (
	_ "github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	_ "github.com/jonathanmkosar/keystone/internal/knowledge/waymarks"
)
