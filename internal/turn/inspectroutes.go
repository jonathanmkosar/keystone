package turn

import "github.com/jonathanmkosar/keystone/internal/api"

// The routes of the turn inspector (P5-04): one turn as the inspector
// shows it, and what a chat's next turn leaves out. internal/app adds them
// to the one list beside Routes. Pinning a message is the pin routes'.

// InspectorRoutes returns the turn inspector's routes, served by r.
func InspectorRoutes(r *Runner) []api.Route {
	const chat = api.Prefix + "chats/{id}"
	const skips = chat + "/skips"
	return []api.Route{
		{
			Method: "GET", Path: chat + "/turns/{turn}/inspect", Handle: r.inspect,
			Summary: "What went into each model call of one turn: every part of the call (standing instructions, tools, profile, person map, the current turn, recalled items with their sources, the summary, pinned messages, earlier turns sent and left out) with its tokens, each call's usage records and cost, and the totals of every usage record of the turn. Also the turn's recall record, the summaries its calls were given, and the chat's pins and what its next turn leaves out.",
			Reply:   InspectReply{},
		},
		{
			Method: "GET", Path: skips, Handle: r.skips,
			Summary: "What the chat's next turn leaves out: recalled pages and sources, and pinned messages, the person took out of it.",
			Reply:   SkipsReply{},
		},
		{
			Method: "PUT", Path: skips + "/{kind}/{ref}", Handle: r.skip,
			Summary: "Leave one item out of the chat's next turn only: a recalled page or source (kind page or source, ref its id in the knowledge store), which a recent turn of the chat must have been given, or a pinned message (kind message, ref its id). It takes no body. Every model call of the next turn leaves it out, and no later turn does. Taking out an item twice changes nothing.",
			Reply:   SkipsReply{},
		},
		{
			Method: "DELETE", Path: skips + "/{kind}/{ref}", Handle: r.unskip,
			Summary: "Put an item back into the chat's next turn. Putting back one that is not left out changes nothing, and an item a turn has already left out stays left out of that turn.",
			Reply:   SkipsReply{},
		},
	}
}
