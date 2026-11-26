package chatcompletions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Embeddings (P6-08). The endpoints this adapter speaks to have a second
// call beside Chat Completions, in the same OpenAI-style shape: POST
// {endpoint}/embeddings with the model and a list of texts, answered with
// one vector for each. LiteLLM, Ollama's compatible server, OpenRouter and
// the Mac mini gateway speak it.
//
//	{"model": "...", "input": ["text one", "text two"], "encoding_format": "float"}
//	{"data": [{"index": 0, "embedding": [0.1, ...]}, ...], "usage": {"prompt_tokens": 12}}
//
// The call is sent again, after a wait, on a failure another attempt may
// not share, as a chat call is. Nothing streams, so a retry never repeats
// anything the caller has seen.

// maxEmbedReply is the largest answer to an embedding call that is read. A
// batch of long vectors is large, so it is larger than one streamed event.
// It is a bound on what is read, not a limit a person tunes.
const maxEmbedReply = 128 << 20

// embedBody is the request as the endpoint reads it.
type embedBody struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format"`
}

// embedReply is the part of the answer this adapter reads.
type embedReply struct {
	Data []struct {
		Index     *int      `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage *struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed turns each text into a vector, as seam.Embedder describes. An
// entry without the embedding capability is refused before anything is
// sent.
func (p *Provider) Embed(ctx context.Context, req seam.EmbedRequest) (seam.EmbedResponse, error) {
	if err := ctx.Err(); err != nil {
		return seam.EmbedResponse{}, err
	}
	if !p.model.Has(models.CapEmbedding) {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the model is not an embedding model: its entry does not have the embedding capability"}
	}
	if err := seam.ValidateEmbedRequest(req); err != nil {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the request is not valid: " + err.Error()}
	}
	body, err := json.Marshal(embedBody{Model: p.model.ProviderModel, Input: req.Texts, EncodingFormat: "float"})
	if err != nil {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the request could not be written"}
	}
	for attempt := 1; ; attempt++ {
		resp, failure, retryAfter, err := p.embedOnce(ctx, req, body)
		if err != nil {
			return seam.EmbedResponse{}, err
		}
		if failure == nil {
			return resp, nil
		}
		failure.Message = p.scrub(failure.Message)
		if !failure.Retryable || attempt >= p.opts.MaxAttempts {
			return seam.EmbedResponse{}, failure
		}
		wait := time.NewTimer(p.backoff(attempt, retryAfter))
		select {
		case <-ctx.Done():
			wait.Stop()
			return seam.EmbedResponse{}, ctx.Err()
		case <-wait.C:
		}
	}
}

// embedURL is where the entry's embedding calls go.
func (p *Provider) embedURL() string {
	return strings.TrimRight(p.model.Endpoint, "/") + "/embeddings"
}

// embedOnce sends the call once. err is the caller's own, the context's;
// failure is the call's.
func (p *Provider) embedOnce(ctx context.Context, req seam.EmbedRequest, body []byte) (seam.EmbedResponse, *seam.Error, time.Duration, error) {
	actx, cancel := context.WithTimeout(ctx, p.opts.ResponseTimeout+p.opts.IdleTimeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, p.embedURL(), bytes.NewReader(body))
	if err != nil {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the entry's endpoint cannot be called"}, 0, nil
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	if p.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.client.Do(hreq)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return seam.EmbedResponse{}, nil, 0, cerr
		}
		return seam.EmbedResponse{}, connectFailure(err, p.opts), 0, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if cerr := ctx.Err(); cerr != nil {
			return seam.EmbedResponse{}, nil, 0, cerr
		}
		return seam.EmbedResponse{}, statusFailure(resp.StatusCode, raw), retryAfter(resp.Header.Get("Retry-After")), nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbedReply+1))
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return seam.EmbedResponse{}, nil, 0, cerr
		}
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the answer broke off before it was complete"}, 0, nil
	}
	if len(raw) > maxEmbedReply {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrOther, Message: "the answer is larger than an embedding answer is read"}, 0, nil
	}
	out, err := readEmbedReply(req, raw)
	if err != nil {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrOther, Message: "the answer is not an embedding answer: " + err.Error()}, 0, nil
	}
	out.Model = req.Model
	return out, nil, 0, nil
}

// readEmbedReply turns the endpoint's answer into the seam's. Each vector
// goes to the text its index names, or, when no item has an index, in the
// order they came. A usage the endpoint does not give is left at zero and
// not marked estimated: the caller, who knows how text is counted, counts.
func readEmbedReply(req seam.EmbedRequest, raw []byte) (seam.EmbedResponse, error) {
	var r embedReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return seam.EmbedResponse{}, errors.New("it is not the JSON of an embedding answer")
	}
	if r.Data == nil {
		if _, _, reason := readError(raw); reason != "" {
			return seam.EmbedResponse{}, errors.New("the endpoint reported an error: " + reason)
		}
		return seam.EmbedResponse{}, errors.New("it has no data")
	}
	if len(r.Data) != len(req.Texts) {
		return seam.EmbedResponse{}, fmt.Errorf("it holds %d vectors for %d texts", len(r.Data), len(req.Texts))
	}
	out := seam.EmbedResponse{Vectors: make([][]float32, len(req.Texts))}
	indexed := r.Data[0].Index != nil
	for i, d := range r.Data {
		at := i
		if indexed {
			if d.Index == nil {
				return seam.EmbedResponse{}, errors.New("some vectors have an index and some do not")
			}
			at = *d.Index
		}
		if at < 0 || at >= len(out.Vectors) || out.Vectors[at] != nil {
			return seam.EmbedResponse{}, fmt.Errorf("a vector has the index %d, which names no text or one named before", at)
		}
		out.Vectors[at] = d.Embedding
	}
	if r.Usage != nil {
		out.Usage.InputTokens = r.Usage.PromptTokens
		if out.Usage.InputTokens == 0 {
			out.Usage.InputTokens = r.Usage.TotalTokens
		}
	}
	if err := seam.ValidateEmbedResponse(req, out); err != nil {
		return seam.EmbedResponse{}, err
	}
	return out, nil
}
