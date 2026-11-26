package turn

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Imported chats (P12-03): a chat that was brought in from another app
// wears a mark in the list of chats and when it is opened, and its replies
// carry the model that app named for them. No turn of Keystone's wrote
// them, so they have no turn id, usage or cost. The record is
// internal/imported's; docs/architecture/import.md is the contract.

// withImports gives each imported chat of a list its mark. A mark that
// cannot be read is left out and logged: the list is not failed for it.
func (r *Runner) withImports(ctx context.Context, s *tenant.Store, chats []Chat) {
	if len(chats) == 0 {
		return
	}
	ids := make([]string, len(chats))
	for i, c := range chats {
		ids[i] = c.ID
	}
	marks, err := imported.Marks(ctx, s, ids)
	if err != nil {
		r.logf("turns: reading the imported chats' marks of tenant %s, so the list has none: %v", s.TenantID(), err)
		return
	}
	for i := range chats {
		if m, ok := marks[chats[i].ID]; ok {
			chats[i].Imported = &m
		}
	}
}

// withImport gives an opened chat its mark, when it was imported, and each
// of its replies that no turn here wrote the model its source named.
func (r *Runner) withImport(ctx context.Context, s *tenant.Store, out *ChatReply) {
	one := []Chat{out.Chat}
	r.withImports(ctx, s, one)
	if one[0].Imported == nil {
		return
	}
	out.Chat = one[0]
	models, err := imported.Models(ctx, s, out.Chat.ID)
	if err != nil {
		r.logf("turns: reading the models of imported chat %s of tenant %s, so its replies name none: %v", out.Chat.ID, s.TenantID(), err)
		return
	}
	for i := range out.Messages {
		if m := &out.Messages[i]; m.TurnID == "" && m.Model == "" {
			m.Model = models[m.ID]
		}
	}
}
