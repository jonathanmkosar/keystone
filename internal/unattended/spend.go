package unattended

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Gate is a run's budget, as the turn runner's gate (turn.Options.Gate,
// through jobs.Gate). The turn runner asks it before each call to a model
// that is not local, after the monthly cap. A turn sent under a run's
// request id is that run's, and the call is not sent when the run's spend
// so far and the most the call could cost are together more than its
// budget. A turn that is no run's is let through.
//
// The answer is the seam's spend_cap error, the cap's own kind, so the
// route moves on as it does at the cap, and Moved decides whether an
// unattended run may go on with a local model.
func Gate(ctx context.Context, s *tenant.Store, turnID string, m models.Model, mostMicros int64) *seam.Error {
	run, ok, err := ForTurn(ctx, s, turnID)
	switch {
	case err != nil:
		return &seam.Error{Kind: seam.ErrSpendCap, Message: "The budget of this unattended work could not be read, so the call was not sent."}
	case !ok:
		return nil
	}
	spent, err := Spent(ctx, s, run.RequestID)
	if err != nil {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: fmt.Sprintf("What the %s has spent could not be read, so the call was not sent.", run.Kind.Noun())}
	}
	if spent+mostMicros > run.BudgetMicros {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: fmt.Sprintf(
			"The call to %s was not sent. The %s “%s” has spent %s of its budget of %s, and this call could cost up to %s.",
			m.Name, run.Kind.Noun(), run.Title, usage.Dollars(spent), usage.Dollars(run.BudgetMicros), usage.Dollars(mostMicros))}
	}
	return nil
}

// The kinds of notice.
const (
	// NoticeFallback says a call the cap or the run's budget refused went on
	// to another model of the route.
	NoticeFallback = "fallback"
	// NoticeStop says a call the cap or the run's budget refused was ended,
	// because the person does not let unattended work fall back.
	NoticeStop = "stop"
)

// Moved is told, through the turn runner (turn.Options.Moved, as
// jobs.Moved), when a model of a turn's route did not answer and the next
// is about to be tried. For a turn that is an unattended run's, and a call
// the monthly cap or the run's budget refused (the seam's spend_cap, with
// nothing sent):
//
//   - when the person lets unattended work fall back (agents.
//     unattended_fallback, on as shipped), the call goes on to the next
//     model and a notice is kept, which the run's chat is told once the
//     round ends, naming the model that wrote the reply (decision 0160);
//   - when they do not, the call is ended with a spend_cap error that says
//     so, and a notice is kept.
//
// Any other turn, and any other failure, is let through: an interactive
// chat at the cap keeps the behaviour decision 0160 gave it. A fallback
// setting that cannot be read is taken as shipped, since going on with a
// model further down the route is gated again and a local one costs
// nothing.
func Moved(ctx context.Context, s *tenant.Store, turnID string, failed routing.Attempt, next models.Model) *seam.Error {
	if failed.Error == nil || failed.Error.Kind != seam.ErrSpendCap || !failed.Refused {
		return nil
	}
	bg := context.Background() // a notice is kept even when the call's context ends
	run, ok, err := ForTurn(bg, s, turnID)
	if err != nil || !ok {
		return nil
	}
	allowed, err := Fallback(bg, s)
	if err != nil {
		allowed = true
	}
	n := noticeRow{requestID: run.RequestID, turnID: turnID, kind: NoticeFallback, model: failed.Model.Name,
		fallbackModel: next.Name, fallbackClass: string(next.Class), message: failed.Error.Message}
	if !allowed {
		n.kind, n.fallbackModel, n.fallbackClass = NoticeStop, "", ""
		n.message = strings.TrimSpace(failed.Error.Message) + fmt.Sprintf(
			" Unattended work does not go on with another model when this happens: that is switched off in your settings, so the %s stopped here.", run.Kind.Noun())
	}
	// A notice that cannot be kept does not stop the call: the refusal is
	// in the audit log either way.
	_ = keep(bg, s, n)
	if !allowed {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: n.message}
	}
	return nil
}

type noticeRow struct {
	seq                     int64
	requestID, turnID, kind string
	model, fallbackModel    string
	fallbackClass, message  string
	noteTurn                string
	at                      time.Time
}

func keep(ctx context.Context, s *tenant.Store, n noticeRow) error {
	_, err := s.DB().ExecContext(ctx, `INSERT INTO unattended_notices(request_id, turn_id, kind, model, fallback_model, fallback_class, message, at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, n.requestID, n.turnID, n.kind, n.model, n.fallbackModel, n.fallbackClass, n.message, stamp())
	return err
}

// Notice is one time a call of a run was refused at the cap or its budget,
// as the routes show it.
type Notice struct {
	// Kind is fallback (another model wrote the reply) or stop.
	Kind string `json:"kind"`
	// TurnID is the round's turn, in the run's chat.
	TurnID string `json:"turn_id"`
	// Model is the model the call was not sent to, and FallbackModel the
	// model that wrote the reply instead.
	Model         string `json:"model"`
	FallbackModel string `json:"fallback_model,omitempty"`
	// Message is the refusal, for a person.
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// told keeps the notices that say what happened: a stop, and a fallback
// whose model then wrote a reply in that turn. A fallback to a model that
// was refused in its turn too is followed by another notice.
const told = `(n.kind = 'stop' OR EXISTS (SELECT 1 FROM turn_messages tm WHERE tm.turn_id = n.turn_id AND tm.model = n.fallback_model))`

func listNotices(ctx context.Context, s *tenant.Store, where string, args ...any) ([]noticeRow, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT n.seq, n.request_id, n.turn_id, n.kind, n.model, n.fallback_model, n.fallback_class, n.message, n.note_turn, n.at
		FROM unattended_notices n WHERE `+where+` ORDER BY n.seq`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []noticeRow
	for rows.Next() {
		var n noticeRow
		var at string
		if err := rows.Scan(&n.seq, &n.requestID, &n.turnID, &n.kind, &n.model, &n.fallbackModel, &n.fallbackClass, &n.message, &n.noteTurn, &at); err != nil {
			return nil, err
		}
		if n.at, err = time.Parse(timeLayout, at); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Notices returns a run's notices that say what happened, oldest first.
func Notices(ctx context.Context, s *tenant.Store, requestID string) ([]Notice, error) {
	got, err := listNotices(ctx, s, "n.request_id = ? AND "+told, requestID)
	if err != nil {
		return nil, err
	}
	out := []Notice{}
	for _, n := range got {
		out = append(out, Notice{Kind: n.kind, TurnID: n.turnID, Model: n.model, FallbackModel: n.fallbackModel, Message: n.message, At: n.at})
	}
	return out, nil
}

// Say is what a notice says to a person: the refusal, then what came of it.
func say(n noticeRow) string {
	text := strings.TrimSpace(n.message)
	if n.kind != NoticeFallback {
		return text
	}
	what := "another model in your fallback chain"
	if n.fallbackClass == string(models.ClassLocal) {
		what = "a local model"
	}
	return fmt.Sprintf("%s So %s, %s, wrote the reply in place of %s.", text, n.fallbackModel, what, n.model)
}

// Summary says in one paragraph what a run's notices came to, for the note
// a task writes in the chat it was started from when it ends: "" when its
// calls were never refused at the cap or its budget.
func Summary(ctx context.Context, s *tenant.Store, requestID string) (string, error) {
	got, err := listNotices(ctx, s, "n.request_id = ? AND n.kind = 'fallback' AND "+told, requestID)
	if err != nil || len(got) == 0 {
		return "", err
	}
	byModel := map[string]bool{}
	var names []string
	for _, n := range got {
		if !byModel[n.fallbackModel] {
			byModel[n.fallbackModel] = true
			names = append(names, n.fallbackModel)
		}
	}
	return fmt.Sprintf("Some of its work was written by %s and not by the model it was given, because a call was refused: %s",
		strings.Join(names, " and "), strings.TrimSpace(got[0].message)), nil
}

// Tell writes, in a run's chat, a note of each time a call of one of its
// turns was refused at the cap or its budget, once the turn has ended:
// write is given the id of the note's turn, chosen and kept before it is
// called so a restart never writes the note twice, and the note's words.
// stops says whether a stop is told too; a run whose own chat is told of
// its end by another note leaves it out. Notices that were not told are
// kept as told, so they are not looked at again.
func Tell(ctx context.Context, s *tenant.Store, requestID, turnID string, stops bool, write func(noteTurn, text string) error) error {
	all, err := listNotices(ctx, s, "n.request_id = ? AND n.turn_id = ? AND n.noted = 0", requestID, turnID)
	if err != nil || len(all) == 0 {
		return err
	}
	shown, err := listNotices(ctx, s, "n.request_id = ? AND n.turn_id = ? AND n.noted = 0 AND "+told, requestID, turnID)
	if err != nil {
		return err
	}
	var lines []string
	seen := map[string]bool{}
	noteTurn := ""
	for _, n := range shown {
		if n.kind == NoticeStop && !stops {
			continue
		}
		if n.noteTurn != "" {
			noteTurn = n.noteTurn
		}
		line := say(n)
		key := n.kind + "\x00" + n.model + "\x00" + n.fallbackModel
		if seen[key] {
			continue
		}
		seen[key] = true
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		if noteTurn == "" {
			if noteTurn, err = newID(); err != nil {
				return err
			}
			if _, err := s.DB().ExecContext(ctx, "UPDATE unattended_notices SET note_turn = ? WHERE request_id = ? AND turn_id = ? AND noted = 0", noteTurn, requestID, turnID); err != nil {
				return err
			}
		}
		if err := write(noteTurn, strings.Join(lines, "\n\n")); err != nil {
			return err
		}
	}
	return markTold(ctx, s.DB(), requestID, turnID)
}

func markTold(ctx context.Context, db *sql.DB, requestID, turnID string) error {
	_, err := db.ExecContext(ctx, "UPDATE unattended_notices SET noted = 1 WHERE request_id = ? AND turn_id = ?", requestID, turnID)
	return err
}
