// Package embedding decides what Keystone turns into vectors, with which
// model, and refreshes the vectors of a person's knowledge store (P6-08).
//
// Waymarks runs no embedding pipeline, and neither does the built-in store:
// Keystone makes the vectors. An embedding model is a registry entry with
// the embedding capability, and this package chooses one, local first.
//
// The package enforces fixed rule 3 for embeddings: content from a
// sensitive space reaches only models the registry marks local or trusted.
// A page is turned into a vector only when MayEmbed says so, Refresh is the
// one way pages reach an embedding model, and it asks MayEmbed of the label
// the store listed and again of the label the page was read with. A chat
// held to local or trusted models sends its query to an embedding model
// only when MayEmbedQuery says so. The package reads no setting: its sizes
// and limits come in as arguments, so no value could switch the rule off.
// tools/verify lists it as a guard and fails if it ever imports the
// settings.
//
// docs/architecture/embeddings.md describes it.
package embedding

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/models"
)

// Kind is the kind of task an embedding call is recorded under in the
// usage records, routing's name for it. The reserve sums it.
const Kind = "embedding"

// The settings a caller reads to fill Options and to run a refresh, in
// settings/knowledge.json. The package itself reads none of them. The
// model is routing.embedding_model, in settings/models.json.
const (
	SettingOn           = "knowledge.embeddings"
	SettingBatchSize    = "knowledge.embedding_batch_size"
	SettingMaxPages     = "knowledge.embedding_pages_per_refresh"
	SettingListMax      = "knowledge.embedding_list_max"
	SettingTextMaxChars = "knowledge.embedding_text_max_chars"
	SettingReserve      = "knowledge.embedding_reserve_usd"
	SettingRefreshEvery = "knowledge.embedding_refresh_every"
	SettingTimeout      = "knowledge.embedding_timeout"
)

var (
	// ErrNoModel means the person has no embedding model Keystone may use:
	// none named, and no local or trusted one.
	ErrNoModel = errors.New("embedding: no embedding model")
	// ErrOptions means the options are out of range.
	ErrOptions = errors.New("embedding: options out of range")
)

// MayEmbed is fixed rule 3 for one page: a page under a label may be sent
// to an embedding model when the label is normal, or when the model may
// read sensitive content. A label that is blank or unknown reads as
// sensitive (decision 0031).
func MayEmbed(label knowledge.Label, m models.Model) bool {
	return !label.Sensitive() || m.MayReadSensitive()
}

// MayEmbedQuery is the same rule for recall's query: a chat that was sent
// sensitive content is held to models that may read it (decision 0211), and
// what the person writes in it next may repeat that content, so its query
// goes only to an embedding model that may read it.
func MayEmbedQuery(held bool, m models.Model) bool {
	return !held || m.MayReadSensitive()
}

// VectorModel is the name a vector made by m carries in a store: the
// provider's own name for the model when the entry has one, and otherwise
// the registry name. Two entries for one provider model, on two endpoints,
// make vectors a store may compare.
func VectorModel(m models.Model) string {
	if p := strings.TrimSpace(m.ProviderModel); p != "" {
		return p
	}
	return m.Name
}

// Choose picks the model that makes a person's vectors, from the models
// they can use. can says whether an entry's adapter makes vectors in this
// build.
//
//   - named is routing.embedding_model. A model of that name with the
//     embedding capability is used, whatever its class: a person who names
//     a hosted model has chosen it, and MayEmbed still keeps sensitive
//     pages from it.
//   - With nothing usable named, the first local embedding model by name,
//     and then the first trusted one. A hosted model is never chosen
//     unasked: what a person keeps is sent to it.
//
// With none, the answer is ErrNoModel, and recall searches by words alone.
func Choose(named string, usable []models.Model, can func(models.Model) bool) (models.Model, error) {
	fits := func(m models.Model) bool { return m.Has(models.CapEmbedding) && (can == nil || can(m)) }
	named = strings.TrimSpace(named)
	sorted := slices.Clone(usable)
	slices.SortFunc(sorted, func(a, b models.Model) int { return strings.Compare(a.Name, b.Name) })
	if named != "" {
		for _, m := range sorted {
			if m.Name == named && fits(m) {
				return m, nil
			}
		}
	}
	for _, class := range []models.Class{models.ClassLocal, models.ClassTrusted} {
		for _, m := range sorted {
			if m.Class == class && fits(m) {
				return m, nil
			}
		}
	}
	if named != "" {
		return models.Model{}, fmt.Errorf("%w: the model %s is not an embedding model this person can use, and there is no local or trusted one", ErrNoModel, named)
	}
	return models.Model{}, fmt.Errorf("%w: there is no local or trusted embedding model, and none is named", ErrNoModel)
}

// PageText is what a page's vector is made from: its title, then its body,
// cut at a word's end to maxChars characters.
func PageText(p knowledge.Page, maxChars int) string {
	text := strings.TrimSpace(strings.TrimSpace(p.Title) + "\n\n" + strings.TrimSpace(strings.ReplaceAll(p.Body, "\r\n", "\n")))
	return cutText(text, maxChars)
}

// cutText cuts a text to n characters, at the end of a word when one ends
// in the last part of it.
func cutText(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	end := len(r)
	for i := len(r) - 1; i > len(r)*3/4; i-- {
		if r[i] == ' ' || r[i] == '\n' {
			end = i
			break
		}
	}
	return strings.TrimSpace(string(r[:end]))
}
