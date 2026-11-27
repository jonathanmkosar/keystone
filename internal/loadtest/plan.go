package loadtest

import (
	"encoding/hex"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The two models every load person has. The hosted one is priced, so the
// load spends against the monthly cap; its entry names an address that can
// never resolve, and the rig gives it the fake provider. The local one is
// the fake adapter itself, with a window a long chat outgrows.
var (
	hostedModel = models.Model{Name: "load-hosted", Label: "Load Hosted", Adapter: models.AdapterChatCompletions,
		Endpoint: "https://models.invalid/v1", ProviderModel: "load-1", ContextWindow: 200000, OutputLimit: 8192,
		Class: models.ClassHosted, Capabilities: []models.Capability{models.CapTools}, Price: models.Price{Input: 1, Output: 5}}
	localModel = models.Model{Name: "load-local", Label: "Load Local", Adapter: models.AdapterFake,
		ContextWindow: 32768, OutputLimit: 4096,
		Class: models.ClassLocal, Capabilities: []models.Capability{models.CapTools}}
)

// The usage a scripted title call reports, in every load.
var titleUsage = seam.Usage{InputTokens: 60, OutputTokens: 8}

// turnPlan is one turn to be: what the person writes, what the model
// answers, when, and what the call reports.
type turnPlan struct {
	user, reply string
	// at is when the person sends it, and took how long the model call
	// lasts.
	at    time.Time
	took  time.Duration
	usage seam.Usage
}

// chatPlan is one chat to be.
type chatPlan struct {
	// person and index say whose chat it is and which of theirs: with the
	// seed they decide everything else.
	person, index int
	model         models.Model
	created       time.Time
	// topic is a few rare words the chat keeps coming back to, and title
	// what the title model answers for it.
	topic []string
	title string
	turns []turnPlan
	// summary and decision are what the chat's recap says.
	summary, decision string
	// rand is the chat's own generator, for the ids the fast path makes.
	rand *rand.Rand
}

// planner makes plans from a profile's seed.
type planner struct {
	p     Profile
	vocab *vocabulary
	// end is the newest moment a generated row may have, and start the
	// oldest for the first person.
	start, end time.Time
}

func newPlanner(p Profile, now time.Time) *planner {
	// A chat's recap is written two minutes after its last turn, so the
	// newest turn ends a few minutes before now.
	end := now.UTC().Truncate(time.Millisecond).Add(-5 * time.Minute)
	return &planner{p: p, vocab: newVocabulary(p.Seed, p.Vocabulary), end: end, start: end.AddDate(0, 0, -p.Days)}
}

// chat plans chat index of count for a person. turns is how many it has,
// or 0 for a number drawn from 1 to twice the mean less one. hosted is
// whether it is on the hosted model, or nil for seven in ten.
func (pl *planner) chat(person, index, count, turns int, model *models.Model) chatPlan {
	r := rand.New(rand.NewPCG(pl.p.Seed, uint64(person)<<32|uint64(index)))
	c := chatPlan{person: person, index: index, rand: r}
	if turns == 0 {
		turns = 1 + r.IntN(2*pl.p.TurnsPerChat-1)
	}
	switch {
	case model != nil:
		c.model = *model
	case r.IntN(10) < 7:
		c.model = hostedModel
	default:
		c.model = localModel
	}
	for i := 0; i < 3; i++ {
		c.topic = append(c.topic, pl.vocab.rare(r))
	}
	c.title = strings.ToUpper(c.topic[0][:1]) + c.topic[0][1:] + " " + c.topic[1] + " " + c.topic[2]

	// The chat's place in the years: chats are spread evenly, oldest first,
	// and one that would run past the end starts early enough not to.
	span := pl.end.Sub(pl.start)
	slot := span / time.Duration(count)
	c.created = pl.start.Add(slot*time.Duration(index) + time.Duration(r.Int64N(int64(slot)/2+1))).Truncate(time.Millisecond)
	at := c.created.Add(5 * time.Second)
	history := 0
	for i := 0; i < turns; i++ {
		words := pl.p.WordsPerTurn*7/10 + r.IntN(pl.p.WordsPerTurn*6/10+1)
		t := turnPlan{
			user:  pl.vocab.text(r, words/5, c.topic),
			reply: pl.vocab.text(r, words*4/5, c.topic),
			took:  time.Duration(800+r.IntN(5200)) * time.Millisecond,
		}
		t.at = at
		at = at.Add(t.took + time.Duration(20+r.IntN(160))*time.Second)
		// What a provider would report: the standing start and the tools,
		// then about a token for every four characters sent and written.
		history += len(t.user) / 4
		t.usage = seam.Usage{InputTokens: 3300 + history, OutputTokens: len(t.reply) / 4}
		history += len(t.reply) / 4
		c.turns = append(c.turns, t)
	}
	c.summary = pl.vocab.text(r, 30+r.IntN(20), c.topic)
	c.decision = pl.vocab.text(r, 10, c.topic)
	if over := at.Sub(pl.end); over > 0 {
		c.shift(over)
	}
	return c
}

// shift moves the whole chat back in time.
func (c *chatPlan) shift(back time.Duration) {
	c.created = c.created.Add(-back)
	for i := range c.turns {
		c.turns[i].at = c.turns[i].at.Add(-back)
	}
}

// id makes an id the way the store does, sixteen hexadecimal characters,
// from the chat's own generator.
func (c *chatPlan) id() string {
	var b [8]byte
	for i := range b {
		b[i] = byte(c.rand.UintN(256))
	}
	return hex.EncodeToString(b[:])
}

// derivedTitle is the title a turn gives a chat when no model writes one:
// the first words of the person's message, tidied as the turn tidies them
// (internal/turn/model.go, tidyTitle).
func derivedTitle(text string, words, most int) string {
	parts := strings.Fields(text)
	if len(parts) > words {
		parts = parts[:words]
	}
	return tidyTitle(strings.Join(parts, " "), most)
}

func tidyTitle(text string, most int) string {
	text = strings.TrimSpace(text)
	if line, _, found := strings.Cut(text, "\n"); found {
		text = line
	}
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, "\"'`*#“”‘’ ")
	text = strings.TrimRight(text, ".,;:")
	if utf8.RuneCountInString(text) > most {
		text = strings.TrimSpace(string([]rune(text)[:most]))
	}
	return text
}
