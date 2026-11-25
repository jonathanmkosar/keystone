package waymarks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// reply is one answer from the instance: its data, and whether Waymarks
// marked it as holding something from a sensitive space.
type reply struct {
	data      json.RawMessage
	sensitive bool
}

// refusal is an answer that is an error: the status, and Waymarks' own code
// and message.
type refusal struct {
	status  int
	code    string
	message string
}

func (r *refusal) Error() string {
	return fmt.Sprintf("Waymarks answered %d %s: %s", r.status, r.code, r.message)
}

// request is one call to make.
type request struct {
	token       string
	method      string
	path        string // under the instance's address, such as /v1/catalog
	query       url.Values
	body        []byte
	contentType string
}

// unavailable is the answer for an instance that cannot be used right now.
func unavailable(format string, args ...any) error {
	return fmt.Errorf("%w: Waymarks %s", knowledge.ErrUnavailable, fmt.Sprintf(format, args...))
}

// scrub takes every token out of a text, so that nothing an instance or a
// proxy echoes back can put a token in an error.
func (s *store) scrub(text string) string {
	for _, token := range []string{s.reader, s.intake, s.embedder} {
		if token != "" {
			text = strings.ReplaceAll(text, token, "(a token)")
		}
	}
	return text
}

// do makes one call. It returns the caller's own error when the caller's
// context is done, a *refusal when the instance answered with an error, and
// ErrUnavailable for everything else that is not an answer: no connection,
// no answer inside the timeout, a redirect, an answer too large, or one
// that is not Waymarks' JSON.
func (s *store) do(ctx context.Context, rq request) (reply, error) {
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}
	timed, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	target := s.base + rq.path
	if len(rq.query) > 0 {
		target += "?" + rq.query.Encode()
	}
	var body io.Reader
	if rq.body != nil {
		body = bytes.NewReader(rq.body)
	}
	req, err := http.NewRequestWithContext(timed, rq.method, target, body)
	if err != nil {
		return reply{}, unavailable("cannot be called at the address in the settings")
	}
	req.Header.Set("Authorization", "Bearer "+rq.token)
	req.Header.Set("Accept", "application/json")
	if rq.contentType != "" {
		req.Header.Set("Content-Type", rq.contentType)
	}

	// failed turns a transport error into the store's answer. The address
	// and the request are left out of it: only the kind of failure is kept.
	failed := func(err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return unavailable("did not answer within %s", s.timeout)
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return unavailable("cannot be reached: %s", s.scrub(err.Error()))
	}

	res, err := s.http.Do(req)
	if err != nil {
		return reply{}, failed(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, s.replyMax+1))
	if err != nil {
		return reply{}, failed(err)
	}
	if int64(len(raw)) > s.replyMax {
		return reply{}, unavailable("sent an answer larger than the %d MB the settings allow", s.replyMax>>20)
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return reply{}, unavailable("sent a redirect, which is not followed: check the address in the settings")
	}

	var env struct {
		Data      json.RawMessage `json:"data"`
		Sensitive bool            `json:"sensitive"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return reply{}, unavailable("sent an answer that is not Waymarks' own (status %d)", res.StatusCode)
	}
	if env.Error != nil {
		return reply{}, &refusal{status: res.StatusCode, code: s.scrub(env.Error.Code), message: s.scrub(env.Error.Message)}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || env.Data == nil {
		return reply{}, unavailable("sent an answer that is not Waymarks' own (status %d)", res.StatusCode)
	}
	return reply{data: env.Data, sensitive: env.Sensitive || res.Header.Get("Waymarks-Sensitive") == "true"}, nil
}

// get makes a GET call and reads the answer's data into v.
func (s *store) get(ctx context.Context, token, path string, query url.Values, v any) (reply, error) {
	return s.into(ctx, request{token: token, method: http.MethodGet, path: path, query: query}, v)
}

// into makes a call and reads the answer's data into v.
func (s *store) into(ctx context.Context, rq request, v any) (reply, error) {
	r, err := s.do(ctx, rq)
	if err != nil {
		return reply{}, err
	}
	if err := json.Unmarshal(r.data, v); err != nil {
		return reply{}, unavailable("sent an answer to %s that is not in the shape Keystone knows", rq.path)
	}
	return r, nil
}

// settle turns the error of a call into one of the store's errors. byID
// says the call named one page or one source.
//
//   - 400 and 413 are a call Waymarks will not take: ErrInvalid.
//   - 409 with the code "stale" is a vector for an old revision: ErrStale.
//   - 404 and 410 for a call by id are a page or source that is not there,
//     or was erased: ErrNotFound.
//   - 403 for a call by id is a page or source in a space the token's grant
//     does not reach. Keystone cannot have it, so for this handle it is not
//     there: ErrNotFound.
//   - Everything else, a refused token included, is ErrUnavailable.
func settle(err error, byID bool) error {
	var r *refusal
	if !errors.As(err, &r) {
		return err
	}
	switch {
	case r.status == http.StatusBadRequest, r.status == http.StatusRequestEntityTooLarge:
		return fmt.Errorf("%w: %s", knowledge.ErrInvalid, r.message)
	case r.status == http.StatusConflict && r.code == "stale":
		return fmt.Errorf("%w: %s", knowledge.ErrStale, r.message)
	case byID && (r.status == http.StatusNotFound || r.status == http.StatusGone || r.status == http.StatusForbidden):
		return fmt.Errorf("%w: %s", knowledge.ErrNotFound, r.message)
	case r.status == http.StatusUnauthorized:
		return unavailable("refused the token: %s", r.message)
	case r.status == http.StatusForbidden:
		return unavailable("refused the call, because the token's grant does not allow it: %s", r.message)
	}
	return unavailable("failed: %s", r.Error())
}

// labelOf turns Waymarks' mark on an answer into a label.
func labelOf(sensitive bool) knowledge.Label {
	if sensitive {
		return knowledge.LabelSensitive
	}
	return knowledge.LabelNormal
}

// labels reads the label of each space the token can read. It is called
// after the content it labels was read, never before: a space made
// sensitive in between then reads as sensitive, which is the safe way to be
// wrong.
type labels map[string]knowledge.Label

func (s *store) labels(ctx context.Context, token string) (labels, error) {
	var spaces []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	if _, err := s.get(ctx, token, "/v1/spaces", nil, &spaces); err != nil {
		return nil, settle(err, false)
	}
	out := labels{}
	for _, sp := range spaces {
		if sp.Label == string(knowledge.LabelNormal) {
			out[sp.Name] = knowledge.LabelNormal
		} else {
			out[sp.Name] = knowledge.LabelSensitive
		}
	}
	return out, nil
}

// of returns a space's label. A space the list does not hold reads as
// sensitive.
func (l labels) of(space string) knowledge.Label {
	if label, ok := l[space]; ok {
		return label
	}
	return knowledge.LabelSensitive
}

// labelsFor returns the labels for an answer that covers several spaces.
// Waymarks marks such an answer when anything in it is from a sensitive
// space. With no mark every space in it is normal, and no second call is
// made. With the mark, the list of spaces says which.
func (s *store) labelsFor(ctx context.Context, token string, r reply) (func(space string) knowledge.Label, error) {
	if !r.sensitive {
		return func(string) knowledge.Label { return knowledge.LabelNormal }, nil
	}
	l, err := s.labels(ctx, token)
	if err != nil {
		return nil, err
	}
	return l.of, nil
}
