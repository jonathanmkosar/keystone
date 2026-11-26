package turn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/embedding"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Embeddings (P6-08). The person's embedding model makes the vectors of
// their knowledge store's pages, in a background refresh, and the vector of
// each message recall searches with. internal/embedding decides which model
// and which pages, and is a guard of fixed rule 3 that reads no setting;
// this file reads the settings, opens the model, records every call as fixed
// rule 4 asks, and asks the embedding reserve and the monthly cap before a
// call that costs anything. docs/architecture/embeddings.md is the whole of
// it.

// ActorEmbeddings is the actor of the background refresh's calls in the
// audit log.
const ActorEmbeddings = "embeddings"

// The bounds of the refresh's timing settings.
const (
	embedEveryLeast   = time.Minute
	embedEveryMost    = 24 * time.Hour
	embedTimeoutLeast = 5 * time.Second
	embedTimeoutMost  = 30 * time.Minute
)

// openedEmbedder is the person's embedding model, opened, with its key for
// the audit log to take out.
type openedEmbedder struct {
	m       models.Model
	e       seam.Embedder
	secrets []string
}

// embedder chooses and opens the person's embedding model:
// routing.embedding_model, or with none usable the first local one, then
// the first trusted one (embedding.Choose). The error wraps
// embedding.ErrNoModel when there is none.
func (r *Runner) embedder(ctx context.Context, s *tenant.Store) (openedEmbedder, error) {
	if err := r.setup(); err != nil {
		return openedEmbedder{}, err
	}
	usable, err := models.Usable(ctx, s)
	if err != nil {
		return openedEmbedder{}, err
	}
	can := func(m models.Model) bool { return r.set[m.Adapter] != nil && adapters.Embeds(m.Adapter) }
	chosen, err := embedding.Choose(r.setting(ctx, s, KeyEmbeddingModel), usable, can)
	if err != nil {
		return openedEmbedder{}, err
	}
	m, e, err := adapters.OpenEmbedder(ctx, s, r.set, chosen.Name)
	if err != nil {
		return openedEmbedder{}, err
	}
	var secrets []string
	if m.Key != "" {
		if v, ok, err := s.Secrets().Get(m.Key); err == nil && ok && v != "" {
			secrets = append(secrets, v)
		}
	}
	return openedEmbedder{m: m, e: e, secrets: secrets}, nil
}

// embedCaller says who an embedding call is made for, in the audit log.
type embedCaller struct {
	requestID, actor string
}

// embedTexts makes one embedding call and records it: a started entry in
// the audit log before it is sent, an end entry with what came back, and a
// usage record of the kind embedding. A call that cannot be recorded is not
// made. Before it is sent, the embedding reserve and then the monthly cap
// are asked, for a model that is not local; a refusal is recorded as
// refused and returned as a *seam.Error of the kind spend_cap.
func (r *Runner) embedTexts(ctx context.Context, s *tenant.Store, who embedCaller, oe openedEmbedder, texts []string) ([][]float32, error) {
	bg := context.Background()
	req := seam.EmbedRequest{Model: oe.m.Name, Texts: texts}
	sent, err := seam.EncodeEmbedRequest(req)
	if err != nil {
		return nil, &seam.Error{Kind: seam.ErrInvalid, Message: "the embedding request is not valid: " + err.Error()}
	}
	start := audit.Entry{Kind: audit.KindModel, RequestID: who.requestID, Actor: who.actor, Action: oe.m.Name, Outcome: audit.OutcomeStarted, Sent: string(sent)}
	if _, err := audit.Append(bg, s, start, oe.secrets...); err != nil {
		return nil, fmt.Errorf("recording the embedding call to %s: %w", oe.m.Name, err)
	}
	end := audit.Entry{Kind: audit.KindModel, RequestID: who.requestID, Actor: who.actor, Action: oe.m.Name}
	finish := func(outcome audit.Outcome, received any, u seam.Usage, cost int64, took time.Duration) {
		raw, _ := json.Marshal(received)
		end.Outcome, end.Received, end.Usage, end.CostMicros, end.DurationMS = outcome, string(raw), audit.Usage(u), cost, took.Milliseconds()
		if _, err := audit.Append(bg, s, end, oe.secrets...); err != nil {
			r.logf("embeddings: %s: recording the end of the call to %s: %v", who.requestID, oe.m.Name, err)
		}
		rec := usage.Record{
			RequestID: who.requestID, Kind: string(routing.KindEmbedding), Model: oe.m.Name, Class: oe.m.Class,
			Outcome: usage.OutcomeError, InputTokens: u.InputTokens, Estimated: u.Estimated, CostMicros: cost, DurationMS: took.Milliseconds(),
		}
		switch outcome {
		case audit.OutcomeOK:
			rec.Outcome = usage.OutcomeOK
		case audit.OutcomeRefused:
			rec.Outcome = usage.OutcomeRefused
		}
		if _, err := usage.Add(bg, s, rec); err != nil {
			r.logf("embeddings: %s: writing the usage record of the call to %s: %v", who.requestID, oe.m.Name, err)
		}
	}
	if refused := r.embedGate(ctx, s, oe.m, texts); refused != nil {
		finish(audit.OutcomeRefused, refused, seam.Usage{}, 0, 0)
		return nil, refused
	}
	began := time.Now()
	resp, err := oe.e.Embed(ctx, req)
	took := time.Since(began)
	if err != nil {
		var se *seam.Error
		if !errors.As(err, &se) {
			se = &seam.Error{Kind: seam.ErrOther, Message: err.Error()}
			if cerr := ctx.Err(); cerr != nil {
				se = &seam.Error{Kind: seam.ErrUnavailable, Message: "the call was stopped: " + cerr.Error()}
			}
		}
		finish(audit.OutcomeError, se, seam.Usage{}, 0, took)
		return nil, err
	}
	if err := seam.ValidateEmbedResponse(req, resp); err != nil {
		se := &seam.Error{Kind: seam.ErrOther, Message: "the embedding answer is not usable: " + err.Error()}
		finish(audit.OutcomeError, se, resp.Usage, 0, took)
		return nil, se
	}
	if resp.Usage.InputTokens == 0 {
		// The endpoint did not count, so Keystone does, as the budget does.
		resp.Usage = seam.Usage{InputTokens: budget.Estimate(textsRequest(oe.m, texts, 0), r.o.Config.Budget), Estimated: true}
	}
	cost := oe.m.Price.CostMicros(resp.Usage.InputTokens, 0, 0, 0)
	finish(audit.OutcomeOK, receivedVectors(resp), resp.Usage, cost, took)
	return resp.Vectors, nil
}

// receivedVectors is what the audit log keeps of an embedding answer
// (decision 0413): the model, the usage, and for each vector its size and
// the SHA-256 of its numbers as little-endian float32 values. The numbers
// themselves are kept by the store; the digest ties a stored vector to the
// call that made it.
func receivedVectors(resp seam.EmbedResponse) any {
	type vector struct {
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	}
	out := struct {
		Model   string     `json:"model"`
		Vectors []vector   `json:"vectors"`
		Usage   seam.Usage `json:"usage"`
	}{Model: resp.Model, Usage: resp.Usage}
	for _, v := range resp.Vectors {
		raw := make([]byte, 4*len(v))
		for i, x := range v {
			binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(x))
		}
		sum := sha256.Sum256(raw)
		out.Vectors = append(out.Vectors, vector{Size: len(v), SHA256: hex.EncodeToString(sum[:])})
	}
	return out
}

// textsRequest is the texts as a seam request, for the budget's estimate
// and the cap's most cost: one message of text blocks, and a reply of at
// most one token, since an embedding model writes none.
func textsRequest(m models.Model, texts []string, maxOutput int) seam.Request {
	var blocks []seam.Block
	for _, t := range texts {
		blocks = append(blocks, seam.Block{Type: seam.BlockText, Text: t})
	}
	return seam.Request{Model: m.Name, Messages: []seam.Message{{Role: seam.RoleUser, Blocks: blocks}}, MaxOutputTokens: maxOutput}
}

// embedGate asks the embedding reserve, and then the monthly cap, about one
// call to a model that is not local. A local model is never asked about. A
// reserve that cannot be read stops the call.
func (r *Runner) embedGate(ctx context.Context, s *tenant.Store, m models.Model, texts []string) *seam.Error {
	if !usage.Counts(m.Class) {
		return nil
	}
	req := textsRequest(m, texts, 1)
	most := mostCost(m, req, r.o.Config.Budget)
	usd, err := strconv.ParseFloat(r.setting(ctx, s, embedding.SettingReserve), 64)
	reserve, rerr := embedding.NewReserve(usd)
	if err != nil || rerr != nil {
		r.logf("embeddings: tenant %s: reading %s: %v %v", s.TenantID(), embedding.SettingReserve, err, rerr)
		return &seam.Error{Kind: seam.ErrSpendCap, Message: "The embedding reserve could not be read, so the call was not sent. The reason is in the server's log."}
	}
	st, err := embedding.Allow(context.Background(), s, reserve, time.Now(), most)
	if err != nil {
		r.logf("embeddings: tenant %s: summing the month's embeddings: %v", s.TenantID(), err)
		return &seam.Error{Kind: seam.ErrSpendCap, Message: "The embedding reserve could not be read, so the call was not sent. The reason is in the server's log."}
	}
	if st.State == usage.StateStopped {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: fmt.Sprintf(
			"The embedding call was not sent to %s. This month's embeddings on models that are not local have cost %s, the embedding reserve is %s, and this call could cost up to %s",
			m.Name, usage.Dollars(st.SpentMicros), usage.Dollars(st.LimitMicros), usage.Dollars(st.NextMicros))}
	}
	return r.capGate(s)(ctx, m, req)
}

// textMaxChars is the most of one text a model is sent: the setting, or
// less when the model's window holds less, as the budget counts.
func (r *Runner) textMaxChars(m models.Model, setting int) int {
	o := r.o.Config.Budget
	if o.CharsPerToken <= 0 || m.ContextWindow <= 0 {
		return setting
	}
	fits := int(float64(m.ContextWindow) * o.CharsPerToken * 100 / float64(100+max(o.MarginPercent, 0)))
	return max(min(setting, fits), 100)
}

// meaningFor returns recall's search by meaning for one turn: the query's
// vector, made by the person's embedding model, the same model the store's
// page vectors come from, and the merge's settings. recall.Find searches by
// meaning beside words when the vector is there.
//
// There is no vector, and recall is by words alone exactly as before, when
// recall by meaning is off, the store takes no query vector, there is no
// embedding model, or the query is empty. There is no vector, with a note
// for the turn inspector, when the chat holds sensitive content and the
// model may not read it, or the call failed or was refused. It never stops
// a turn.
func (r *Runner) meaningFor(ctx context.Context, s *tenant.Store, t row, ks knowledge.Store, adapter string, o recall.Options, timeout time.Duration) recall.Meaning {
	none := recall.Meaning{}
	if ks == nil || !ks.Capabilities().VectorSearch || r.setting(ctx, s, recall.SettingMeaning) != "true" {
		return none
	}
	query := recall.Query(newestText(s, t), o)
	if query == "" || o.MaxItems == 0 {
		return none
	}
	oe, err := r.embedder(ctx, s)
	if err != nil {
		if !errors.Is(err, embedding.ErrNoModel) {
			r.logf("turns: %s turn %s: recall: the embedding model did not open, so recall searches by words alone: %v", t.requestID, t.ID, err)
			return recall.Meaning{Note: "The embedding model did not open, so recall searched by words alone. The reason is in the server's log."}
		}
		return none
	}
	// Fixed rule 3: what the person writes in a chat that holds sensitive
	// content may repeat it, so its query goes only to a model that may
	// read it.
	if !embedding.MayEmbedQuery(r.sensitive(ctx, s, t, true), oe.m) {
		return recall.Meaning{Note: "This chat holds sensitive content and the embedding model " + oe.m.Name + " is not local or trusted, so the message was not sent to it, and recall searched by words alone."}
	}
	m, err := r.meaningOptions(ctx, s, adapter)
	if err != nil {
		r.logf("turns: %s turn %s: recall: %v", t.requestID, t.ID, err)
		return recall.Meaning{Note: "The settings of recall by meaning do not read, so recall searched by words alone. The reason is in the server's log."}
	}
	ectx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	text := cutQuery(query, r.textMaxChars(oe.m, len(query)))
	vectors, err := r.embedTexts(ectx, s, embedCaller{requestID: t.requestID, actor: t.actor}, oe, []string{text})
	if err != nil {
		if ctx.Err() == nil {
			r.logf("turns: %s turn %s: recall: the message's vector was not made, so recall searches by words alone: %v", t.requestID, t.ID, err)
		}
		return recall.Meaning{Note: "The message's vector was not made, so recall searched by words alone. The reason is in the server's log."}
	}
	m.Vector, m.Model = vectors[0], embedding.VectorModel(oe.m)
	return m
}

// cutQuery cuts the query to n characters.
func cutQuery(q string, n int) string {
	r := []rune(q)
	if len(r) <= n {
		return q
	}
	return string(r[:n])
}

// meaningOptions reads the merge's settings for one adapter's store.
func (r *Runner) meaningOptions(ctx context.Context, s *tenant.Store, adapter string) (recall.Meaning, error) {
	var bad []string
	number := func(key string) float64 {
		n, err := strconv.ParseFloat(r.setting(ctx, s, key), 64)
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	m := recall.Meaning{
		MaxResults:    int(number(recall.SettingMeaningResults)),
		MinScore:      number(recall.SettingMeaningMinScore(adapter)),
		WordsWeight:   number(recall.SettingMergeWordsWeight),
		MeaningWeight: number(recall.SettingMergeMeaningWeight),
		RankConstant:  number(recall.SettingMergeRankConstant),
	}
	if len(bad) > 0 {
		return recall.Meaning{}, fmt.Errorf("these settings do not read: %v", bad)
	}
	check := m
	check.Vector, check.Model = []float32{1}, "check"
	if err := recall.CheckOptions(recall.Options{SearchResults: 1, ItemMaxChars: 100, QueryMaxChars: 1, Meaning: check}); err != nil {
		return recall.Meaning{}, err
	}
	return m, nil
}

// refreshing holds one lock for each runner and tenant, so one person's
// vectors are refreshed by one refresh at a time: the sweep's or a route's.
// It is a package-level map so the runner needs no new field.
var refreshing sync.Map // "<runner>/<tenant id>" to *sync.Mutex

func (r *Runner) refreshLock(tenantID string) *sync.Mutex {
	mu, _ := refreshing.LoadOrStore(fmt.Sprintf("%p/%s", r, tenantID), &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// refreshOutcome is what refreshing one person's vectors came to.
type refreshOutcome struct {
	adapter string
	model   *openedEmbedder
	caps    knowledge.Capabilities
	state   embedding.State
	ran     bool
	note    string
}

// refreshTenant refreshes one person's page vectors now: the pages with
// none of the embedding model's, or a stale one, get one, sensitive pages
// only from a model that may read them. With no store, the switch off, no
// embedding model, or a store that takes no vectors, nothing is sent, and
// the outcome says why. A refresh that ran keeps its state.
func (r *Runner) refreshTenant(ctx context.Context, s *tenant.Store, who embedCaller) refreshOutcome {
	if err := r.setup(); err != nil {
		r.logf("embeddings: setting up the runner: %v", err)
		return refreshOutcome{note: "Keystone could not read its settings, so no vectors were made. The reason is in the server's log."}
	}
	out := refreshOutcome{adapter: r.setting(ctx, s, knowledge.SettingAdapter)}
	if out.adapter == "" || out.adapter == knowledge.None {
		out.note = "You have no knowledge store, so there is nothing to make vectors of."
		return out
	}
	oe, err := r.embedder(ctx, s)
	if err != nil {
		out.note = "You have no embedding model Keystone may use: name one in routing.embedding_model, or add a local or trusted model with the embedding capability. Recall searches by words alone."
		if !errors.Is(err, embedding.ErrNoModel) {
			r.logf("embeddings: tenant %s: opening the embedding model: %v", s.TenantID(), err)
			out.note = "The embedding model did not open, so no vectors were made. The reason is in the server's log."
		}
		return out
	}
	out.model = &oe
	ks, err := knowledge.Open(ctx, out.adapter, s)
	if err != nil {
		r.logf("embeddings: tenant %s: the knowledge store %s did not open: %v", s.TenantID(), out.adapter, err)
		out.note = "The knowledge store did not open, so no vectors were made. The reason is in the server's log."
		return out
	}
	defer ks.Close()
	out.caps = ks.Capabilities()
	if !out.caps.PageVectors {
		out.note = "Your knowledge store does not take page vectors, so recall searches it by words alone."
		if out.adapter == "waymarks" {
			out.note += " For Waymarks, store an embedder token as knowledge.waymarks.embedder."
		}
		return out
	}
	if r.setting(ctx, s, embedding.SettingOn) != "true" {
		out.note = "Making vectors is switched off in knowledge.embeddings."
		return out
	}
	o, timeout, err := r.refreshOptions(ctx, s)
	if err != nil {
		r.logf("embeddings: tenant %s: %v", s.TenantID(), err)
		out.note = "The settings of the refresh do not read, so no vectors were made. The reason is in the server's log."
		return out
	}
	o.TextMaxChars = r.textMaxChars(oe.m, o.TextMaxChars)
	embed := func(ctx context.Context, texts []string) ([][]float32, error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return r.embedTexts(cctx, s, who, oe, texts)
	}
	res, err := embedding.Refresh(ctx, ks, oe.m, embed, o)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("embeddings: tenant %s: the refresh stopped: %v", s.TenantID(), err)
		}
		res.Stopped = "the knowledge store failed; the reason is in the server's log"
	}
	out.ran = true
	out.state = embedding.State{Adapter: out.adapter, At: time.Now(), Class: string(oe.m.Class), Result: res, Note: embedding.Note(res)}
	out.note = out.state.Note
	if err := embedding.Save(context.Background(), s, out.state); err != nil {
		r.logf("embeddings: tenant %s: keeping the refresh's state: %v", s.TenantID(), err)
	}
	if res.Held > 0 {
		r.logf("embeddings: tenant %s: %d pages in a sensitive space have no vector, because the embedding model %s is not local or trusted", s.TenantID(), res.Held, oe.m.Name)
	}
	return out
}

// refreshOptions reads the refresh's limits and the timeout of one call.
func (r *Runner) refreshOptions(ctx context.Context, s *tenant.Store) (embedding.Options, time.Duration, error) {
	var bad []string
	whole := func(key string) int {
		n, err := strconv.Atoi(r.setting(ctx, s, key))
		if err != nil {
			bad = append(bad, key)
		}
		return n
	}
	o := embedding.Options{
		BatchSize: whole(embedding.SettingBatchSize), MaxPages: whole(embedding.SettingMaxPages),
		ListMax: whole(embedding.SettingListMax), TextMaxChars: whole(embedding.SettingTextMaxChars),
	}
	timeout, err := time.ParseDuration(r.setting(ctx, s, embedding.SettingTimeout))
	if err != nil || timeout < embedTimeoutLeast || timeout > embedTimeoutMost {
		bad = append(bad, embedding.SettingTimeout)
	}
	if len(bad) > 0 {
		return embedding.Options{}, 0, fmt.Errorf("these settings do not read: %v", bad)
	}
	if err := embedding.CheckOptions(o); err != nil {
		return embedding.Options{}, 0, err
	}
	return o, timeout, nil
}

// SweepEmbeddings starts the background refresh: every
// knowledge.embedding_refresh_every, and once now, each person's missing
// and stale page vectors are made. It stops when the runner is closed. The
// server calls it once, when it starts.
func (r *Runner) SweepEmbeddings() {
	if r.o.Install == nil || r.setup() != nil {
		return
	}
	every := 10 * time.Minute
	if st, ok := settings.Lookup(r.reg, embedding.SettingRefreshEvery); ok {
		if d, err := time.ParseDuration(st.Default); err == nil && d >= embedEveryLeast && d <= embedEveryMost {
			every = d
		} else {
			r.logf("embeddings: the setting %s does not read as a time from %s to %s, so the refresh runs every %s", embedding.SettingRefreshEvery, embedEveryLeast, embedEveryMost, every)
		}
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			r.SweepEmbeddingsNow()
			select {
			case <-r.base.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// SweepEmbeddingsNow refreshes every person's page vectors once, and
// returns how many vectors it made. A person whose refresh is already
// running is passed over.
func (r *Runner) SweepEmbeddingsNow() int {
	if r.o.Install == nil || r.setup() != nil {
		return 0
	}
	n := 0
	for _, t := range r.o.Install.Tenants() {
		if r.isClosing() {
			return n
		}
		mu := r.refreshLock(t.ID)
		if !mu.TryLock() {
			continue
		}
		s, err := r.o.Install.Store(t.ID)
		if err != nil {
			mu.Unlock()
			r.logf("embeddings: the refresh: opening tenant %s: %v", t.ID, err)
			continue
		}
		out := r.refreshTenant(r.base, s, embedCaller{requestID: refreshRequestID(), actor: ActorEmbeddings})
		n += out.state.Embedded
		s.Close()
		mu.Unlock()
	}
	return n
}

// refreshRequestID names one background refresh in the audit log, where no
// API call set it off.
func refreshRequestID() string {
	var b [8]byte
	rand.Read(b[:])
	return "embeddings-" + hex.EncodeToString(b[:])
}

// EmbeddingModelInfo is the embedding model as GET /knowledge/embeddings
// shows it.
type EmbeddingModelInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Class string `json:"class"`
	// VectorModel is the name the vectors carry in the store.
	VectorModel string `json:"vector_model"`
	// MayReadSensitive says pages in a sensitive space are embedded too.
	MayReadSensitive bool `json:"may_read_sensitive"`
}

// EmbeddingsReply answers GET /knowledge/embeddings and POST
// /knowledge/embeddings/refresh: where the person's vectors stand.
type EmbeddingsReply struct {
	// Adapter is the person's knowledge store.
	Adapter string `json:"adapter"`
	// On is knowledge.embeddings.
	On bool `json:"on"`
	// Model is the embedding model, or nothing when there is none.
	Model *EmbeddingModelInfo `json:"model,omitempty"`
	// TakesVectors says the store takes page vectors, and SearchesByMeaning
	// that recall searches it by meaning: the store takes a query vector,
	// there is a model, and knowledge.recall_meaning is on.
	TakesVectors      bool `json:"takes_vectors"`
	SearchesByMeaning bool `json:"searches_by_meaning"`
	// Last is the last refresh that ran, for this store.
	Last *embedding.State `json:"last,omitempty"`
	// Refreshed says this answer's refresh ran now (POST only).
	Refreshed bool `json:"refreshed"`
	// Note is what the person is told: why nothing is made, or what was
	// left without a vector and why.
	Note string `json:"note"`
}

// EmbeddingRoutes returns the routes of embeddings (P6-08), served by r.
func EmbeddingRoutes(r *Runner) []api.Route {
	const base = api.Prefix + "knowledge/embeddings"
	return []api.Route{
		{
			Method: "GET", Path: base, Handle: r.embeddingsStatus,
			Summary: "Where the person's page vectors stand: the knowledge store, the embedding model and its class, whether recall searches by meaning, and the last refresh, with how many pages in a sensitive space were left without a vector because the model is not local or trusted.",
			Reply:   EmbeddingsReply{},
		},
		{
			Method: "POST", Path: base + "/refresh", Handle: r.embeddingsRefresh,
			Summary: "Refresh the person's page vectors now, as the background refresh does: the pages with no vector of the embedding model's, or a stale one, get one, a page in a sensitive space only from a local or trusted model. It takes no body.",
			Reply:   EmbeddingsReply{},
		},
	}
}

func (r *Runner) embeddingsStatus(w http.ResponseWriter, req *http.Request, c api.Call) {
	api.WriteJSON(w, http.StatusOK, r.embeddingsReply(req.Context(), c.Store, nil))
}

func (r *Runner) embeddingsRefresh(w http.ResponseWriter, req *http.Request, c api.Call) {
	mu := r.refreshLock(c.Store.TenantID())
	mu.Lock()
	out := r.refreshTenant(req.Context(), c.Store, embedCaller{requestID: c.RequestID, actor: api.ActorPerson})
	mu.Unlock()
	api.WriteJSON(w, http.StatusOK, r.embeddingsReply(req.Context(), c.Store, &out))
}

// embeddingsReply works out where a person's vectors stand. ran is the
// refresh this answer made, when it made one.
func (r *Runner) embeddingsReply(ctx context.Context, s *tenant.Store, ran *refreshOutcome) EmbeddingsReply {
	if err := r.setup(); err != nil {
		r.logf("embeddings: setting up the runner: %v", err)
		return EmbeddingsReply{Note: "Keystone could not read its settings. The reason is in the server's log."}
	}
	reply := EmbeddingsReply{On: r.setting(ctx, s, embedding.SettingOn) == "true"}
	out := ran
	if out == nil {
		// A look without a refresh: the same checks, and nothing sent.
		look := refreshOutcome{adapter: r.setting(ctx, s, knowledge.SettingAdapter)}
		if look.adapter != "" && look.adapter != knowledge.None {
			if oe, err := r.embedder(ctx, s); err == nil {
				look.model = &oe
			}
			if ks, err := knowledge.Open(ctx, look.adapter, s); err == nil {
				look.caps = ks.Capabilities()
				ks.Close()
			}
		}
		out = &look
	}
	reply.Adapter = out.adapter
	reply.TakesVectors = out.caps.PageVectors
	if out.model != nil {
		m := out.model.m
		reply.Model = &EmbeddingModelInfo{Name: m.Name, Label: m.Label, Class: string(m.Class), VectorModel: embedding.VectorModel(m), MayReadSensitive: m.MayReadSensitive()}
		reply.SearchesByMeaning = out.caps.VectorSearch && r.setting(ctx, s, recall.SettingMeaning) == "true"
	}
	if reply.Adapter != "" && reply.Adapter != knowledge.None {
		if st, ok, err := embedding.Load(ctx, s, reply.Adapter); err == nil && ok {
			reply.Last = &st
		}
	}
	reply.Refreshed = ran != nil && ran.ran
	switch {
	case ran != nil:
		reply.Note = ran.note
	case reply.Adapter == "" || reply.Adapter == knowledge.None:
		reply.Note = "You have no knowledge store, so there is nothing to make vectors of."
	case reply.Model == nil:
		reply.Note = "You have no embedding model Keystone may use: name one in routing.embedding_model, or add a local or trusted model with the embedding capability. Recall searches by words alone."
	case !reply.TakesVectors:
		reply.Note = "Your knowledge store does not take page vectors, so recall searches it by words alone."
	case reply.Last != nil:
		reply.Note = reply.Last.Note
	}
	return reply
}
