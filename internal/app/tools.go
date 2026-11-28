package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/browser"
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
	// A search tries the person's own providers, then the install's, kept
	// in the data folder's admin/ (P7-06).
	c.Web.Install = c.Install
	if err := web.Add(r, c.Web); err != nil {
		return nil, err
	}
	// Background tasks (P8-01): a model may start one. Starting one
	// changes something, so it asks unless the person sets it to always.
	// A caller with no task runner, such as a check that makes its own
	// registry from Defaults, gets no such tool.
	if c.jobs != nil {
		if err := c.jobs.AddTool(r); err != nil {
			return nil, err
		}
	}
	// Artifacts (P9-03): a model makes, changes and reads the artifacts of
	// its chat. They change nothing outside Keystone, so they run without
	// asking unless the person sets them to (decision 0420).
	if err := artifacts.Add(r, c.Artifacts); err != nil {
		return nil, err
	}
	// Sub-agents (P8-02): a model starts several at once and waits for their
	// results. A caller with no sub-agent runner gets no such tool.
	if c.subagents != nil {
		if err := c.subagents.AddTool(r); err != nil {
			return nil, err
		}
	}
	// Browser use (P10-03): the agent's own headless browser, offered only to
	// a person who switches it on. Clicking and typing change something, so
	// they ask. Each person's browser closes after its idle time, and with
	// Keystone.
	if _, err := browser.Add(r, c.Browser); err != nil {
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
