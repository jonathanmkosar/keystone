package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/mcp"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Pool keeps open sessions with people's connectors, so a stdio server is
// not started again for each call and a remote one is not initialized
// again. A session is opened when a tool of the connector is first called,
// is kept while it is used, and is closed after Options.IdleTimeout of no
// use. A connector whose entry or token changed gets a new session; one that
// was removed or switched off has its session closed the next time that
// person's turn asks for tools. Sessions are kept by tenant and connector,
// and one person's session is never used for another.
//
// The server makes one Pool and closes it when it stops (Close). A nil Pool
// offers nothing.
type Pool struct {
	o    Options
	logf func(format string, args ...any)

	mu       sync.Mutex
	closed   bool
	sessions map[string]*session  // tenant/connector
	retry    map[string]time.Time // tenant/connector: not listed at offer time before this
}

type session struct {
	key         string
	fingerprint string
	ready       chan struct{} // closed when the open has finished
	client      *mcp.Client
	err         error
	redact      func(string) string
	users       int
	idle        *time.Timer
	gone        bool
}

// NewPool returns an empty pool. logf is the server's log; nil means none.
func NewPool(o Options, logf func(format string, args ...any)) *Pool {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Pool{o: o, logf: logf, sessions: map[string]*session{}, retry: map[string]time.Time{}}
}

// Close closes every session. A call after it fails.
func (p *Pool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	all := p.sessions
	p.sessions = map[string]*session{}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-s.ready
			if s.idle != nil {
				s.idle.Stop()
			}
			if s.client != nil {
				s.client.Close()
			}
		}()
	}
	wg.Wait()
}

func key(s *tenant.Store, name string) string { return s.TenantID() + "/" + name }

// errPoolClosed is a call after the server began to stop.
var errPoolClosed = errors.New("Keystone is stopping, so the connector was not reached")

// acquire returns an open session with a connector, opening one when there
// is none or the one there was opened with another entry or token. The
// caller releases it.
func (p *Pool) acquire(ctx context.Context, s *tenant.Store, c Connector) (*session, error) {
	r, err := p.o.reachFor(s, c)
	if err != nil {
		return nil, err
	}
	if r.transport == nil {
		return nil, errors.New("it is not reached through a session") // a Google connector (P12-02)
	}
	k := key(s, c.Name)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errPoolClosed
	}
	if old, ok := p.sessions[k]; ok && old.fingerprint != r.fingerprint {
		p.dropLocked(old)
	}
	ses, ok := p.sessions[k]
	if !ok {
		ses = &session{key: k, fingerprint: r.fingerprint, ready: make(chan struct{}), redact: r.redact}
		p.sessions[k] = ses
		go p.open(s, c.Name, ses, r)
	}
	ses.users++
	if ses.idle != nil {
		ses.idle.Stop()
	}
	p.mu.Unlock()

	select {
	case <-ses.ready:
	case <-ctx.Done():
		p.release(ses)
		return nil, ctx.Err()
	}
	if ses.err != nil {
		p.release(ses)
		return nil, ses.err
	}
	return ses, nil
}

// open opens a session beside the call that asked for it, so a call that
// is stopped does not leave it half open, and keeps the tools it listed.
func (p *Pool) open(s *tenant.Store, name string, ses *session, r reach) {
	ctx := context.Background()
	client, listed, err := p.o.connect(ctx, r)
	if err != nil {
		ses.err = errors.New(r.redactText(err.Error()))
	}
	// The store is the turn's. A turn that was stopped while this opened
	// may have closed it, and then what was listed is not kept this time.
	problem := ""
	if err != nil {
		problem = ses.err.Error()
	}
	if serr := SaveTools(ctx, s, name, listed, problem, p.o.MaxTools); serr != nil {
		p.logf("connectors: tenant %s: keeping what %s listed: %v", s.TenantID(), name, serr)
	}
	p.mu.Lock()
	ses.client = client
	if err != nil {
		p.dropLocked(ses)
	}
	p.mu.Unlock()
	close(ses.ready)
	if err == nil && p.isClosed() {
		client.Close()
	}
}

func (p *Pool) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// release says a call is done with a session. A session no call uses is
// closed after the idle timeout.
func (p *Pool) release(ses *session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ses.users--
	if ses.users > 0 || ses.gone {
		if ses.gone && ses.users == 0 {
			p.closeLater(ses)
		}
		return
	}
	if ses.idle != nil {
		ses.idle.Stop()
	}
	ses.idle = time.AfterFunc(p.o.IdleTimeout, func() {
		p.mu.Lock()
		if ses.users > 0 || ses.gone {
			p.mu.Unlock()
			return
		}
		p.dropLocked(ses)
		p.mu.Unlock()
	})
}

// dropLocked takes a session out of the pool, so the next call opens a new
// one, and closes it once no call uses it. p.mu is held.
func (p *Pool) dropLocked(ses *session) {
	if cur, ok := p.sessions[ses.key]; ok && cur == ses {
		delete(p.sessions, ses.key)
	}
	if ses.gone {
		return
	}
	ses.gone = true
	if ses.users == 0 {
		p.closeLater(ses)
	}
}

// closeLater closes a session's client beside the caller, once it is open.
func (p *Pool) closeLater(ses *session) {
	go func() {
		<-ses.ready
		if ses.client != nil {
			ses.client.Close()
		}
	}()
}

// sweep closes the sessions of one person's connectors that are gone or
// switched off.
func (p *Pool) sweep(s *tenant.Store, kept []Kept) {
	on := map[string]bool{}
	for _, k := range kept {
		if k.Enabled {
			on[key(s, k.Name)] = true
		}
	}
	prefix := s.TenantID() + "/"
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, ses := range p.sessions {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix && !on[k] {
			p.dropLocked(ses)
		}
	}
}

// call calls one tool of a connector. A server that forgot the session is
// given a new one and the call is made once more: the server did not take
// it. A session whose server went away is dropped, and the call is not made
// again, since the server may have taken it.
func (p *Pool) call(ctx context.Context, s *tenant.Store, c Connector, tool string, args json.RawMessage) (mcp.CallResult, func(string) string, error) {
	for try := 0; ; try++ {
		ses, err := p.acquire(ctx, s, c)
		if err != nil {
			return mcp.CallResult{}, redactor(""), err
		}
		res, err := ses.client.CallTool(ctx, tool, args)
		if errors.Is(err, mcp.ErrSessionGone) || errors.Is(err, mcp.ErrClosed) {
			p.mu.Lock()
			p.dropLocked(ses)
			p.mu.Unlock()
		} else if err == nil && ses.client.ToolsChanged() {
			go p.relist(s, c.Name, ses)
		}
		p.release(ses)
		if errors.Is(err, mcp.ErrSessionGone) && try == 0 {
			continue
		}
		if err != nil {
			err = errors.New(ses.redact(err.Error()))
		}
		return res, ses.redact, err
	}
}

// relist keeps a connector's tools again, after its server said they
// changed.
func (p *Pool) relist(s *tenant.Store, name string, ses *session) {
	ctx, cancel := context.WithTimeout(context.Background(), p.o.ConnectTimeout)
	defer cancel()
	listed, err := ses.client.ListTools(ctx)
	if err != nil {
		return
	}
	if err := SaveTools(ctx, s, name, listed, "", p.o.MaxTools); err != nil {
		p.logf("connectors: tenant %s: keeping what %s listed: %v", s.TenantID(), name, err)
	}
}

// listNow reaches a connector that has never listed its tools, at offer
// time, unless an earlier try failed less than Options.RetryAfter ago. It
// returns the connector as kept after.
func (p *Pool) listNow(ctx context.Context, s *tenant.Store, k Kept) Kept {
	if k.Transport == TransportGoogle {
		// Keystone's own tools: kept without reaching anything (P12-02).
		if err := saveGoogleTools(ctx, s, k.Connector, p.o.MaxTools); err != nil {
			p.logf("connectors: tenant %s: keeping %s's tools: %v", s.TenantID(), k.Name, err)
		}
		if got, err := Get(ctx, s, k.Name); err == nil {
			return got
		}
		return k
	}
	rk := key(s, k.Name)
	p.mu.Lock()
	wait := time.Now().Before(p.retry[rk])
	p.mu.Unlock()
	if wait {
		return k
	}
	ses, err := p.acquire(ctx, s, k.Connector)
	if err == nil {
		p.release(ses)
	} else {
		p.mu.Lock()
		p.retry[rk] = time.Now().Add(p.o.RetryAfter)
		p.mu.Unlock()
	}
	if got, err := Get(ctx, s, k.Name); err == nil {
		return got
	}
	return k
}
