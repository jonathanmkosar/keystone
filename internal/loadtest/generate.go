package loadtest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/uploads"
)

// batch is how many chats the fast path writes in one transaction.
const batch = 50

// load is what the generator left, as the measurements need it.
type load struct {
	// long is the first person's long chat, and attachable two of their
	// ordinary chats, each with a recap.
	long       written
	attachable []written
}

// ownTrace is the test's own count of what one request set off: its model
// calls, their tokens and their cost.
type ownTrace struct {
	calls               int
	input, output, cost int64
}

// generate fills every person's folder: the first person's years of chats
// and their long chat, and each other person's smaller load, with recaps,
// uploads and artifacts in proportion. say is told how it goes.
func (g *rig) generate(ctx context.Context, say func(string)) (*load, error) {
	p, first := g.p, g.people[0]

	// One real turn, the first person's, gives the fast path what a plain
	// turn's model call carries beside the chat's messages.
	probe, err := g.newChat(first)
	if err != nil {
		return nil, err
	}
	r := rand.New(rand.NewPCG(p.Seed, 0x70726f6265)) // "probe"
	got, err := g.send(first, probe, turn.SendBody{Text: g.pl.vocab.text(r, 20, nil), Model: localModel.Name})
	if err != nil {
		return nil, err
	}
	tpl, err := g.templateFrom(ctx, first.store, got.requestID)
	if err != nil {
		return nil, err
	}

	out := &load{}
	for _, who := range g.people {
		who.generated = &tally{traces: map[string]*ownTrace{}}
		f := newFast(tpl, who.store, who.generated)
		f.recaps = true
		count := p.TenantChats
		if who.index == 0 {
			count = p.Chats
		}
		began := time.Now()
		for from := 0; from < count; from += batch {
			var plans []chatPlan
			for i := from; i < min(from+batch, count); i++ {
				plans = append(plans, g.pl.chat(who.index, i, count, 0, nil))
			}
			wrote, err := f.write(ctx, plans)
			if err != nil {
				return nil, err
			}
			for i, w := range wrote {
				if err := g.extras(ctx, who, plans[i], w); err != nil {
					return nil, err
				}
			}
			if who.index == 0 && len(out.attachable) < 2 {
				out.attachable = append(out.attachable, wrote[:min(2, len(wrote))]...)
			}
			if say != nil && who.index == 0 && (from/batch)%10 == 9 {
				say(fmt.Sprintf("%s: %d of %d chats, %d turns, %s", who.name, min(from+batch, count), count, who.generated.turns, time.Since(began).Round(time.Second)))
			}
		}
		if who.index == 0 {
			// The long chat, on the local model, whose window it outgrows.
			// It is some weeks old, so its calls are not the last day's.
			long := g.pl.chat(0, count, count+1, p.LongChatTurns, &localModel)
			long.shift(40 * 24 * time.Hour)
			wrote, err := f.write(ctx, []chatPlan{long})
			if err != nil {
				return nil, err
			}
			out.long = wrote[0]
		}
		if say != nil {
			t := who.generated
			say(fmt.Sprintf("%s: %d chats, %d turns, %d messages, %d model calls, %d uploads, %d artifacts in %s",
				who.name, t.chats, t.turns, t.messages, len(t.calls), t.uploads, t.artifacts, time.Since(began).Round(time.Millisecond)))
		}
	}
	if len(out.attachable) < 2 {
		return nil, fmt.Errorf("the profile makes %d chats for the first person, and the measurements attach two", len(out.attachable))
	}
	return out, nil
}

// extras gives a written chat its upload and its artifact, when its place
// among the person's chats says it has one, through the store's own
// functions.
func (g *rig) extras(ctx context.Context, who *person, c chatPlan, w written) error {
	p, t := g.p, who.generated
	if p.UploadEvery > 0 && c.index%p.UploadEvery == p.UploadEvery-1 {
		// From a tenth of the mean size to about twice it.
		size := p.UploadKB * 1024 * (1 + c.rand.IntN(20)) / 10
		data := []byte(g.fill(c, size))
		_, added, err := uploads.Add(ctx, who.store, g.cfg.Uploads, w.id, "notes-"+strconv.Itoa(c.index+1)+".txt", data)
		if err != nil {
			return fmt.Errorf("adding an upload to chat %d: %w", c.index, err)
		}
		if added {
			t.uploads++
			t.fileBytes += onDisk(who, len(data))
		}
	}
	if p.ArtifactEvery > 0 && c.index%p.ArtifactEvery == p.ArtifactEvery-1 {
		size := p.ArtifactKB * 1024 * (1 + c.rand.IntN(20)) / 10
		one := "# " + c.title + "\n\n" + g.fill(c, size) + "\n"
		a, _, err := artifacts.Create(ctx, who.store, g.cfg.Artifacts, w.id, artifacts.Draft{Type: artifacts.KindMarkdown, Title: c.title, Content: one}, w.lastReq)
		if err != nil {
			return fmt.Errorf("making an artifact in chat %d: %w", c.index, err)
		}
		two := one + "\n" + g.fill(c, size/10+40) + "\n"
		if _, _, err := artifacts.Update(ctx, who.store, g.cfg.Artifacts, w.id, a.ID, artifacts.Change{Content: &two}, w.lastReq); err != nil {
			return fmt.Errorf("changing the artifact of chat %d: %w", c.index, err)
		}
		t.artifacts++
		t.artifactVersions += 2
		t.artifactBytes += onDisk(who, len(one)) + onDisk(who, len(two))
	}
	return nil
}

// fill is about size bytes of the chat's invented words.
func (g *rig) fill(c chatPlan, size int) string {
	s := g.pl.vocab.text(c.rand, size/7+8, c.topic)
	for len(s) < size {
		s += "\n\n" + g.pl.vocab.text(c.rand, (size-len(s))/7+8, c.topic)
	}
	return s[:size]
}
