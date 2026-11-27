package loadtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The fast path writes the rows a real turn leaves, with dates of its own.
//
// The real paths take no date: the chat record, the turn, the audit log and
// the usage records all stamp a row with the moment it is written. A load
// written through them would hold years of chats made in one minute, and
// every call would sit in Operations' last day and the usage report's
// current month. So the fast path writes the same rows directly, in large
// transactions, in the order a real turn writes them:
//
//   - chats, messages and their search rows, as internal/chat's Create,
//     Append and Rename leave them;
//   - turns, turn_events (every streamed piece, as the fake provider cuts
//     them), turn_messages, turn_contexts and chat_styles, as internal/turn
//     leaves them;
//   - one usage record and two audit entries for each model call, and two
//     audit entries for each API call, as internal/usage and internal/audit
//     leave them.
//
// What a model call carries is not written by hand. The request and the
// record of what it was built from come from the real context builder,
// contextbuild.Build, given what a plain turn gives it; an event's data is
// the turn package's own type, marshalled; a message is the seam's own
// encoding. TestTheFastPathLeavesWhatARealTurnLeaves sends a sample of
// chats through the real handler, writes the same sample here, and compares
// the two databases row by row (agree.go).

// Times are stored as every part of Keystone stores them.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stampOf(t time.Time) string { return t.UTC().Format(timeLayout) }

// template is what a plain turn gives the context builder beside the
// chat's messages, read from a real turn of the same person and from their
// settings.
type template struct {
	tools     []seam.Tool
	options   contextbuild.Options
	budget    budget.Options
	maxOutput int
	// style is the style a chat's first turn keeps for it.
	style string
	// delta is how many characters the provider streams in one piece.
	delta int
	// The title call's settings, and the chat record's limit on a title.
	titlePrompt                                  string
	titleMaxTokens, titleInputChars, titleLength int
	titleDerivedWords                            int
	recap                                        recap.Options
}

// templateFrom reads the template from the model call a real turn made
// under requestID, in the person's own audit log.
func (g *rig) templateFrom(ctx context.Context, s *tenant.Store, requestID string) (*template, error) {
	entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
	if err != nil {
		return nil, err
	}
	var sent string
	for _, e := range entries {
		if e.Outcome == audit.OutcomeStarted && e.Sent != "" {
			sent = e.Sent
			break
		}
	}
	if sent == "" {
		return nil, fmt.Errorf("the audit log holds no model call under request %s", requestID)
	}
	req, err := seam.DecodeRequest([]byte(sent))
	if err != nil {
		return nil, fmt.Errorf("the model call under request %s does not read: %w", requestID, err)
	}
	get := func(key string) (string, bool) {
		v, err := tenantsettings.Get(ctx, s, g.reg, key)
		return v, err == nil
	}
	opts, err := contextbuild.FromSettings(g.reg, get)
	if err != nil {
		return nil, err
	}
	number := func(key string) int {
		v, _ := get(key)
		n, _ := strconv.Atoi(v)
		return n
	}
	text := func(key string) string { v, _ := get(key); return v }
	// The words on charts and diagrams follow the standing text, as the turn
	// runner adds them to every call (P9-04, turn/visuals.go).
	if draw, _ := strconv.ParseBool(text(turn.KeyVisualsDraw)); draw {
		if words := strings.TrimSpace(text(turn.KeyVisualsInstructions)); words != "" {
			if opts.SystemText == "" {
				opts.SystemText = words
			} else {
				opts.SystemText += "\n\n" + words
			}
		}
	}
	c := g.cfg.Turns
	t := &template{
		tools: req.Tools, options: opts, budget: c.Budget, maxOutput: max(number(turn.KeyMaxOutputTokens), 0),
		style: "normal", delta: 12,
		titlePrompt: c.TitlePrompt, titleMaxTokens: c.TitleMaxTokens, titleInputChars: c.TitleInputChars,
		titleLength: c.Limits.TitleLength, titleDerivedWords: c.TitleDerivedWords,
		recap: recap.Options{
			Prompt:          text(recap.SettingPrompt),
			SummaryMaxWords: number(recap.SettingSummaryMaxWords), MaxTopics: number(recap.SettingMaxTopics),
			MaxDecisions: number(recap.SettingMaxDecisions), ItemMaxChars: number(recap.SettingItemMaxChars),
			MessageMaxChars: number(recap.SettingMessageMaxChars), InputMaxChars: number(recap.SettingInputMaxChars),
			MaxOutputTokens: number(recap.SettingMaxOutputTokens),
		},
	}
	if n := number("fake.delta_size"); n > 0 {
		t.delta = n
	}
	if v := text("styles.default"); v != "" {
		t.style = v
	}
	return t, recap.CheckOptions(t.recap)
}

// genCall is one model call the fast path wrote.
type genCall struct {
	at         time.Time
	model      string
	class      models.Class
	kind       string
	costMicros int64
	durationMS int64
	tokens     int
}

// tally is the generator's own count of what it wrote for one person.
type tally struct {
	chats, turns, messages int
	calls                  []genCall
	// The uploads and artifacts written through the store functions
	// (extras.go), and their bytes on disk.
	uploads, artifacts, artifactVersions int
	fileBytes, artifactBytes             int64
	recaps                               int
	// traces is what each request set off, by its id, when it is kept.
	traces map[string]*ownTrace
}

// written is one chat the fast path wrote.
type written struct {
	id        string
	lastID    int64 // its last message
	turns     int
	lastReq   string // the request id of its last turn
	lastEnded time.Time
	title     string
	// leftOut is how many earlier turns the context builder left out of
	// the chat's last call: more than none means the chat has outgrown its
	// model's window.
	leftOut int
	// recap is the chat's recap, to be kept once the batch is written.
	recap *recap.Recap
}

// fast writes chats for one person.
type fast struct {
	tpl   *template
	s     *tenant.Store
	tally *tally
	// recaps says each chat gets a recap, and the model call that wrote
	// it. A real turn writes none itself: the recap job does, two idle
	// minutes later (internal/turn/recap.go).
	recaps bool
	tx     *sql.Tx
	stmts  map[string]*sql.Stmt
}

func newFast(tpl *template, s *tenant.Store, t *tally) *fast {
	return &fast{tpl: tpl, s: s, tally: t}
}

// write writes a batch of chats in one transaction, in order.
func (f *fast) write(ctx context.Context, chats []chatPlan) ([]written, error) {
	tx, err := f.s.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	f.tx, f.stmts = tx, map[string]*sql.Stmt{}
	defer func() {
		for _, st := range f.stmts {
			st.Close()
		}
		f.tx, f.stmts = nil, nil
	}()
	out := make([]written, 0, len(chats))
	for i := range chats {
		w, err := f.chat(ctx, &chats[i])
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("writing chat %d: %w", chats[i].index, err)
		}
		out = append(out, w)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// The recaps, through the store's own function, which keeps the search
	// index of recaps in step.
	for _, w := range out {
		if w.recap == nil {
			continue
		}
		if _, err := recap.Save(ctx, f.s, *w.recap); err != nil {
			return nil, fmt.Errorf("keeping the recap of chat %s: %w", w.id, err)
		}
		f.tally.recaps++
	}
	return out, nil
}

// exec runs one insert or update of the batch, prepared once.
func (f *fast) exec(query string, args ...any) (sql.Result, error) {
	st := f.stmts[query]
	if st == nil {
		var err error
		if st, err = f.tx.Prepare(query); err != nil {
			return nil, err
		}
		f.stmts[query] = st
	}
	return st.Exec(args...)
}

// entry is one audit entry to write.
type entry struct {
	at                     time.Time
	kind                   audit.Kind
	requestID, action      string
	outcome                audit.Outcome
	status                 int
	sent, received         string
	usage                  seam.Usage
	costMicros, durationMS int64
}

func (f *fast) audit(e entry) error {
	_, err := f.exec(`INSERT INTO audit_log(
			at, kind, request_id, actor, action, outcome, status, sent, received,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			thinking_tokens, tokens_estimated, cost_micros, duration_ms)
		VALUES(?, ?, ?, 'person', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stampOf(e.at), string(e.kind), e.requestID, e.action, string(e.outcome), e.status, e.sent, e.received,
		e.usage.InputTokens, e.usage.OutputTokens, e.usage.CacheReadTokens, e.usage.CacheWriteTokens,
		e.usage.ThinkingTokens, e.usage.Estimated, e.costMicros, e.durationMS)
	return err
}

// call writes one model call: its two audit entries and its usage record,
// as internal/turn's call and used leave them.
func (f *fast) call(at time.Time, took time.Duration, requestID, kind string, m models.Model, req seam.Request, resp seam.Response) (int64, error) {
	sent, err := seam.EncodeRequest(req)
	if err != nil {
		return 0, err
	}
	received, err := json.Marshal(resp)
	if err != nil {
		return 0, err
	}
	u := resp.Usage
	cost := m.Price.CostMicros(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	ended, ms := at.Add(took), took.Milliseconds()
	if err := f.audit(entry{at: at, kind: audit.KindModel, requestID: requestID, action: m.Name, outcome: audit.OutcomeStarted, sent: string(sent)}); err != nil {
		return 0, err
	}
	if err := f.audit(entry{at: ended, kind: audit.KindModel, requestID: requestID, action: m.Name, outcome: audit.OutcomeOK,
		received: string(received), usage: u, costMicros: cost, durationMS: ms}); err != nil {
		return 0, err
	}
	_, err = f.exec(`INSERT INTO usage_records(
			at, month, request_id, kind, model, class, outcome,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			thinking_tokens, tokens_estimated, cost_micros, duration_ms)
		VALUES(?, ?, ?, ?, ?, ?, 'ok', ?, ?, ?, ?, ?, ?, ?, ?)`,
		stampOf(ended), usage.MonthOf(ended), requestID, kind, m.Name, string(m.Class),
		u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.ThinkingTokens, u.Estimated, cost, ms)
	if err != nil {
		return 0, err
	}
	f.tally.calls = append(f.tally.calls, genCall{at: ended.UTC().Truncate(time.Millisecond), model: m.Name, class: m.Class, kind: kind,
		costMicros: cost, durationMS: ms, tokens: u.InputTokens + u.OutputTokens})
	if f.tally.traces != nil {
		tr := f.tally.traces[requestID]
		if tr == nil {
			tr = &ownTrace{}
			f.tally.traces[requestID] = tr
		}
		tr.calls++
		tr.input, tr.output, tr.cost = tr.input+int64(u.InputTokens), tr.output+int64(u.OutputTokens), tr.cost+cost
	}
	return cost, nil
}

// message writes one message under parent, as chat.Append leaves it, with
// what its turn recorded about it, and returns its id.
func (f *fast) message(chatID string, parent int64, m seam.Message, at time.Time, turnID string, reply *seam.Usage, model string, cost int64) (int64, error) {
	content, err := seam.EncodeMessage(m)
	if err != nil {
		return 0, err
	}
	if _, err := f.exec("UPDATE chats SET active_at = ? WHERE id = ?", stampOf(at), chatID); err != nil {
		return 0, err
	}
	res, err := f.exec("INSERT INTO messages(chat_id, parent_id, role, content, created_at) VALUES(?, ?, ?, ?, ?)",
		chatID, sql.NullInt64{Int64: parent, Valid: parent != 0}, string(m.Role), string(content), stampOf(at))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if parent != 0 {
		_, err = f.exec("UPDATE messages SET current_child = ? WHERE id = ?", id, parent)
	} else {
		_, err = f.exec("UPDATE chats SET current_root = ? WHERE id = ?", id, chatID)
	}
	if err != nil {
		return 0, err
	}
	if _, err := f.exec("INSERT INTO message_search(rowid, body) VALUES(?, ?)", id, seam.Text(m)); err != nil {
		return 0, err
	}
	stop, used := "", ""
	if reply != nil {
		raw, err := json.Marshal(reply)
		if err != nil {
			return 0, err
		}
		stop, used = string(seam.StopEnd), string(raw)
	}
	_, err = f.exec("INSERT INTO turn_messages(message_id, turn_id, model, stop_reason, usage, cost_micros) VALUES(?, ?, ?, ?, ?, ?)",
		id, turnID, model, stop, used, cost)
	f.tally.messages++
	return id, err
}

// chat writes one chat: the call that made it, and each of its turns.
func (f *fast) chat(ctx context.Context, c *chatPlan) (written, error) {
	tpl := f.tpl
	chatID, madeBy := c.id(), c.id()
	if err := f.audit(entry{at: c.created, kind: audit.KindAPI, requestID: madeBy, action: "POST /api/v1/chats", outcome: audit.OutcomeStarted}); err != nil {
		return written{}, err
	}
	res, err := f.exec("INSERT INTO chats(id, created_at, active_at) VALUES(?, ?, ?)", chatID, stampOf(c.created), stampOf(c.created))
	if err != nil {
		return written{}, err
	}
	chatSeq, err := res.LastInsertId()
	if err != nil {
		return written{}, err
	}
	if err := f.audit(entry{at: c.created.Add(time.Millisecond), kind: audit.KindAPI, requestID: madeBy, action: "POST /api/v1/chats",
		outcome: audit.OutcomeOK, status: 201, durationMS: 1}); err != nil {
		return written{}, err
	}
	f.tally.chats++

	out := written{id: chatID, turns: len(c.turns)}
	var parent int64
	var history []seam.Message
	m := c.model
	action := "POST /api/v1/chats/" + chatID + "/turns"
	for i, t := range c.turns {
		turnID, requestID := c.id(), c.id()
		created, started := t.at.Add(time.Millisecond), t.at.Add(5*time.Millisecond)
		asked, opened := t.at.Add(6*time.Millisecond), t.at.Add(10*time.Millisecond)
		answered := opened.Add(t.took)
		if err := f.audit(entry{at: t.at, kind: audit.KindAPI, requestID: requestID, action: action, outcome: audit.OutcomeStarted}); err != nil {
			return written{}, err
		}
		body := turn.SendBody{Text: t.user, Model: m.Name}
		input, err := json.Marshal(body)
		if err != nil {
			return written{}, err
		}
		if _, err := f.exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'queued', ?, 'person', ?, ?)",
			turnID, chatID, requestID, string(input), stampOf(created)); err != nil {
			return written{}, err
		}
		// Every event the turn streams, in order, as the turn's own types.
		seq := int64(0)
		emit := func(name string, data any) error {
			raw, err := json.Marshal(data)
			if err != nil {
				return err
			}
			seq++
			_, err = f.exec("INSERT INTO turn_events(turn_id, seq, name, data) VALUES(?, ?, ?, ?)", turnID, seq, name, string(raw))
			return err
		}
		state := turn.Turn{ID: turnID, ChatID: chatID, Status: turn.StatusQueued, Created: created.UTC()}
		emitTurn := func(name string) error {
			state.LastEventID = seq + 1
			return emit(name, state)
		}
		if err := emitTurn(turn.EventTurn); err != nil {
			return written{}, err
		}
		at := started.UTC()
		state.Status, state.Model, state.Started = turn.StatusRunning, m.Name, &at
		if err := emitTurn(turn.EventTurn); err != nil {
			return written{}, err
		}

		// The person's message.
		userMessage := seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: t.user}}}
		userID, err := f.message(chatID, parent, userMessage, asked, turnID, nil, "", 0)
		if err != nil {
			return written{}, err
		}
		if err := emit(turn.EventMessage, turn.Message{ID: userID, ChatID: chatID, ParentID: parent, Role: seam.RoleUser,
			Blocks: userMessage.Blocks, Created: asked.UTC(), Siblings: []int64{userID}, TurnID: turnID}); err != nil {
			return written{}, err
		}
		if i == 0 {
			if _, err := f.exec("INSERT INTO chat_styles(chat_id, style_id, chosen_at) VALUES(?, ?, ?)", chatID, tpl.style, stampOf(asked)); err != nil {
				return written{}, err
			}
		}

		// The model call, built by the real context builder.
		history = append(history, userMessage)
		req, rec, err := contextbuild.Build(ctx, contextbuild.Input{
			Model: m, Budget: tpl.budget, Options: tpl.options, MaxOutputTokens: tpl.maxOutput,
			Messages: history, Tools: tpl.tools,
		})
		if err != nil {
			return written{}, err
		}
		record, err := json.Marshal(rec)
		if err != nil {
			return written{}, err
		}
		out.leftOut = rec.Part(contextbuild.PartRecent).Dropped
		if _, err := f.exec("INSERT INTO turn_contexts(turn_id, step, record) VALUES(?, 1, ?)", turnID, string(record)); err != nil {
			return written{}, err
		}
		reply := seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: t.reply}}}
		resp := seam.Response{Model: m.Name, Message: reply, StopReason: seam.StopEnd, Usage: t.usage}
		cost, err := f.call(opened, t.took, requestID, "chat", m, req, resp)
		if err != nil {
			return written{}, err
		}
		if err := emit(turn.EventStart, turn.StartEvent{Step: 1, Model: m.Name}); err != nil {
			return written{}, err
		}
		for _, piece := range pieces(t.reply, tpl.delta) {
			if err := emit(turn.EventText, turn.DeltaEvent{Step: 1, Index: 0, Text: piece}); err != nil {
				return written{}, err
			}
		}
		u := t.usage
		replyID, err := f.message(chatID, userID, reply, answered, turnID, &u, m.Name, cost)
		if err != nil {
			return written{}, err
		}
		if err := emit(turn.EventMessage, turn.Message{ID: replyID, ChatID: chatID, ParentID: userID, Role: seam.RoleAssistant,
			Blocks: reply.Blocks, Created: answered.UTC(), Siblings: []int64{replyID}, TurnID: turnID,
			Model: m.Name, StopReason: string(seam.StopEnd), Usage: &u, CostMicros: cost}); err != nil {
			return written{}, err
		}
		if err := emit(turn.EventUsage, turn.UsageEvent{Step: 1, Model: m.Name, StopReason: string(seam.StopEnd), Usage: u, CostMicros: cost}); err != nil {
			return written{}, err
		}
		history = append(history, reply)
		ended := answered.Add(2 * time.Millisecond)

		// The chat's title, after its first reply: a model's own adapter is
		// asked for one, and the fake adapter's chats take the first words
		// of the person's message.
		if i == 0 {
			title := derivedTitle(t.user, tpl.titleDerivedWords, tpl.titleLength)
			if m.Adapter != models.AdapterFake {
				titleReq := seam.Request{
					Model:  m.Name,
					System: []seam.Block{{Type: seam.BlockText, Text: tpl.titlePrompt}},
					Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText,
						Text: fmt.Sprintf("The person wrote:\n%s\n\nThe reply began:\n%s", firstRunes(t.user, tpl.titleInputChars), firstRunes(t.reply, tpl.titleInputChars))}}}},
					MaxOutputTokens: tpl.titleMaxTokens,
				}
				titleResp := seam.Response{Model: m.Name, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: c.title}}},
					StopReason: seam.StopEnd, Usage: titleUsage}
				if _, err := f.call(ended, 400*time.Millisecond, requestID, "title", m, titleReq, titleResp); err != nil {
					return written{}, err
				}
				ended = ended.Add(402 * time.Millisecond)
				title = tidyTitle(c.title, tpl.titleLength)
			}
			if title != "" {
				if _, err := f.exec("UPDATE chats SET title = ? WHERE id = ?", title, chatID); err != nil {
					return written{}, err
				}
				if _, err := f.exec("INSERT INTO chat_title_search(rowid, title) VALUES(?, ?)", chatSeq, title); err != nil {
					return written{}, err
				}
				if err := emit(turn.EventTitle, turn.TitleEvent{ChatID: chatID, Title: title}); err != nil {
					return written{}, err
				}
				out.title = title
			}
		}

		// The turn's end.
		done := ended.UTC()
		state.Status, state.UserMessageID, state.ReplyMessageID = turn.StatusDone, userID, replyID
		state.StopReason, state.Usage, state.CostMicros, state.Ended = string(seam.StopEnd), u, cost, &done
		if err := emitTurn(turn.EventEnd); err != nil {
			return written{}, err
		}
		total, err := json.Marshal(u)
		if err != nil {
			return written{}, err
		}
		if _, err := f.exec(`UPDATE turns SET status = 'done', model = ?, user_message_id = ?, reply_message_id = ?, stop_reason = 'end',
				usage = ?, cost_micros = ?, last_event = ?, started_at = ?, ended_at = ? WHERE id = ?`,
			m.Name, userID, replyID, string(total), cost, seq, stampOf(started), stampOf(ended), turnID); err != nil {
			return written{}, err
		}
		if err := f.audit(entry{at: ended.Add(time.Millisecond), kind: audit.KindAPI, requestID: requestID, action: action,
			outcome: audit.OutcomeOK, status: 200, durationMS: ended.Add(time.Millisecond).Sub(t.at).Milliseconds()}); err != nil {
			return written{}, err
		}
		parent = replyID
		out.lastID, out.lastReq, out.lastEnded = replyID, requestID, ended
		f.tally.turns++
	}
	if len(c.turns) == 0 {
		return written{}, errors.New("a chat needs a turn")
	}
	if !f.recaps {
		return out, nil
	}
	// The chat's recap, written once the chat has rested: the request is
	// the recap package's own, from the whole chat, and the call is
	// recorded as a summary call under the last turn's request id.
	req, err := recap.Request(recap.Ask{Options: tpl.recap, Model: m.Name, Title: out.title, Messages: history})
	if err != nil {
		return written{}, err
	}
	answer, err := json.Marshal(map[string]any{"summary": c.summary, "topics": c.topic, "decisions": []string{c.decision}})
	if err != nil {
		return written{}, err
	}
	resp := seam.Response{Model: m.Name, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: string(answer)}}},
		StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: (len(tpl.recap.Prompt) + len(seam.Text(req.Messages[0]))) / 4, OutputTokens: len(answer) / 4}}
	if _, err := f.call(out.lastEnded.Add(2*time.Minute), 2*time.Second, out.lastReq, "summary", m, req, resp); err != nil {
		return written{}, err
	}
	out.recap = &recap.Recap{ChatID: chatID, Summary: c.summary, Topics: c.topic, Decisions: []string{c.decision},
		ThroughID: out.lastID, Turns: len(c.turns), Model: m.Name, RequestID: out.lastReq}
	return out, nil
}

// pieces cuts text as the fake provider streams it: size characters a
// piece.
func pieces(s string, size int) []string {
	if size <= 0 || utf8.RuneCountInString(s) <= size {
		return []string{s}
	}
	var out []string
	runes := []rune(s)
	for len(runes) > size {
		out = append(out, string(runes[:size]))
		runes = runes[size:]
	}
	return append(out, string(runes))
}

func firstRunes(text string, n int) string {
	if utf8.RuneCountInString(text) > n {
		return string([]rune(text)[:n])
	}
	return text
}
