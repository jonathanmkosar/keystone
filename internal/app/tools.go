package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// Tools returns the registry of the tools this server's turns may use,
// with the built-in tools in it. This is where the server's tools are put
// together: an item that brings tools adds them here, by a plain call on
// the registry, as builtin.Add does.
func Tools(c Config) (*tools.Registry, error) {
	r := tools.New()
	if err := builtin.Add(r, builtin.Options{Limits: c.Turns.Limits, ReadChatMessages: c.Turns.ToolLoop.ReadChatMessages}); err != nil {
		return nil, err
	}
	// Web search and fetch (P7-03): both only read, and both bring outside
	// content into a turn.
	if err := web.Add(r, c.Web); err != nil {
		return nil, err
	}
	return r, nil
}

// toolsOrNone is Tools for a caller that cannot fail: a registry that
// could not be filled is logged and the server runs with no tools.
func (c Config) toolsOrNone(log *api.Log) *tools.Registry {
	r, err := Tools(c)
	if err != nil {
		log.Printf("tools: the built-in tools could not be registered, so no tool is offered: %v", err)
		return tools.New()
	}
	return r
}
