package embedding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Options are the limits of one refresh. A caller reads them from settings
// and passes them in.
type Options struct {
	// BatchSize is how many pages go in one embedding call.
	BatchSize int
	// MaxPages is the most pages one refresh sends to the model.
	MaxPages int
	// ListMax is the most pages the store is asked to list as needing a
	// vector. It is larger than MaxPages, because the pages a model may not
	// read are listed and passed over.
	ListMax int
	// TextMaxChars is the most characters of one page the model is sent.
	TextMaxChars int
}

// CheckOptions refuses limits no refresh can run with.
func CheckOptions(o Options) error {
	switch {
	case o.BatchSize < 1 || o.BatchSize > 256:
		return fmt.Errorf("%w: the batch size must be from 1 to 256", ErrOptions)
	case o.MaxPages < 1 || o.MaxPages > 100000:
		return fmt.Errorf("%w: the most pages a refresh embeds must be from 1 to 100000", ErrOptions)
	case o.ListMax < 1 || o.ListMax > 100000:
		return fmt.Errorf("%w: the most pages listed must be from 1 to 100000", ErrOptions)
	case o.TextMaxChars < 100 || o.TextMaxChars > 1000000:
		return fmt.Errorf("%w: the most characters of a page must be from 100 to 1000000", ErrOptions)
	}
	return nil
}

// EmbedFunc makes one vector for each text, in order. The caller records
// the call, asks the spend reserve and the cap first, and returns a
// *seam.Error of the kind spend_cap when either refuses.
type EmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)

// Result is what one refresh did.
type Result struct {
	// Model is the registry name of the model that made the vectors, and
	// VectorModel the name the vectors carry.
	Model       string `json:"model"`
	VectorModel string `json:"vector_model"`
	// Listed is how many pages the store listed as needing a vector.
	Listed int `json:"listed"`
	// Embedded is how many pages got a vector.
	Embedded int `json:"embedded"`
	// Held is how many pages were left without one because they are in a
	// sensitive space and the model may not read sensitive content.
	Held int `json:"held"`
	// Stale is how many pages changed while their vector was made. The
	// next refresh makes it again.
	Stale int `json:"stale"`
	// Failed is how many pages could not be read, or whose vectors the
	// model or the store refused.
	Failed int `json:"failed"`
	// Waiting is how many pages the model may read were left for the next
	// refresh, past MaxPages or after the refresh stopped.
	Waiting int `json:"waiting"`
	// Stopped says why the refresh stopped before it had done what was
	// listed. Empty when it did not.
	Stopped string `json:"stopped,omitempty"`
}

// ErrNoVectors means the store takes no page vectors: it does not have the
// PageVectors capability.
var ErrNoVectors = errors.New("embedding: the knowledge store takes no page vectors")

// Refresh makes the vectors a person's store is missing, or holds stale,
// with the model m, and stores them. It is the one way a page reaches an
// embedding model:
//
//  1. The store lists the pages with no vector of m's, or a stale one.
//  2. A page the model may not read, by the label the store listed it
//     with, is passed over and counted as held. It is never read.
//  3. The rest are read, a batch at a time, and a page the model may not
//     read by the label it was read with is held too: a space made
//     sensitive between the list and the read.
//  4. The batch's texts go to embed, and each vector to the store, for the
//     revision that was read. A page that changed meanwhile is stale and
//     waits for the next refresh.
//
// A call that fails, a refusal by the reserve or the cap, and a store that
// refuses a vector stop the refresh: what was stored stays, and the rest
// waits. The error is the store's or the context's; a failed embedding
// call is in the result.
func Refresh(ctx context.Context, ks knowledge.Store, m models.Model, embed EmbedFunc, o Options) (Result, error) {
	res := Result{Model: m.Name, VectorModel: VectorModel(m)}
	if err := CheckOptions(o); err != nil {
		return res, err
	}
	if !m.Has(models.CapEmbedding) {
		return res, fmt.Errorf("%w: the model %s does not have the embedding capability", ErrNoModel, m.Name)
	}
	if ks == nil || !ks.Capabilities().PageVectors {
		return res, ErrNoVectors
	}
	needs, err := ks.VectorsNeeded(ctx, res.VectorModel, o.ListMax)
	if err != nil {
		return res, err
	}
	res.Listed = len(needs)
	var queue []knowledge.PageNeed
	for _, n := range needs {
		switch {
		case !MayEmbed(n.Label, m):
			res.Held++
		case len(queue) >= o.MaxPages:
			res.Waiting++
		default:
			queue = append(queue, n)
		}
	}
	for start := 0; start < len(queue); start += o.BatchSize {
		batch := queue[start:min(start+o.BatchSize, len(queue))]
		if res.Stopped != "" {
			res.Waiting += len(batch)
			continue
		}
		var pages []knowledge.Page
		var texts []string
		for _, n := range batch {
			p, err := ks.ReadPage(ctx, n.PageID)
			if err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return res, cerr
				}
				if errors.Is(err, knowledge.ErrUnavailable) {
					return res, err
				}
				res.Failed++ // removed since it was listed, or unreadable
				continue
			}
			if !MayEmbed(p.Label, m) || !MayEmbed(n.Label, m) {
				res.Held++
				continue
			}
			text := PageText(p, o.TextMaxChars)
			if text == "" {
				res.Failed++
				continue
			}
			pages, texts = append(pages, p), append(texts, text)
		}
		if len(texts) == 0 {
			continue
		}
		vectors, err := embed(ctx, texts)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return res, cerr
			}
			res.Stopped = stoppedBy(err)
			res.Waiting += len(texts)
			continue
		}
		if len(vectors) != len(texts) {
			res.Stopped = fmt.Sprintf("the model gave %d vectors for %d pages", len(vectors), len(texts))
			res.Failed += len(texts)
			continue
		}
		for i, p := range pages {
			err := ks.PutVector(ctx, knowledge.PageVector{PageID: p.ID, Revision: p.Revision, Model: res.VectorModel, Vector: vectors[i]})
			switch {
			case err == nil:
				res.Embedded++
			case errors.Is(err, knowledge.ErrStale), errors.Is(err, knowledge.ErrNotFound):
				res.Stale++
			case ctx.Err() != nil:
				return res, ctx.Err()
			case errors.Is(err, knowledge.ErrInvalid):
				// A store set up for another model or size, such as a
				// Waymarks instance whose owner chose another model.
				res.Failed += len(pages) - i
				res.Stopped = "the knowledge store refused the vectors: " + oneLine(err.Error())
			default:
				return res, err
			}
			if res.Stopped != "" {
				break
			}
		}
	}
	return res, nil
}

// stoppedBy says why an embedding call stopped a refresh.
func stoppedBy(err error) string {
	var se *seam.Error
	if errors.As(err, &se) {
		if se.Kind == seam.ErrSpendCap {
			return oneLine(se.Message)
		}
		return fmt.Sprintf("the embedding call failed (%s): %s", se.Kind, oneLine(se.Message))
	}
	return "the embedding call failed: " + oneLine(err.Error())
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
