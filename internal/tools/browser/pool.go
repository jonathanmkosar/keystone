package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// Pool keeps each person's browser: at most one at a time, with one tab,
// started on the first call and closed after the idle time. A browser's
// tab belongs to the chat that last opened a page in it; another chat must
// open its own page first.
type Pool struct {
	o      Options
	mu     sync.Mutex
	people map[string]*person
	hosts  map[string]hostVerdict
	closed bool
}

type person struct {
	tenantID string
	mu       sync.Mutex // one call at a time
	b        Browser
	profile  string
	owner    string // the chat whose page the tab shows
	idle     *time.Timer
	gone     bool
}

type hostVerdict struct {
	err error
	at  time.Time
}

// hostCache is how long a host's verdict is kept.
const hostCache = time.Minute

// NewPool returns a pool that starts browsers with o.
func NewPool(o Options) *Pool {
	return &Pool{o: o, people: map[string]*person{}, hosts: map[string]hostVerdict{}}
}

// Close closes every browser and removes its profile.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	all := make([]*person, 0, len(p.people))
	for _, s := range p.people {
		all = append(all, s)
	}
	p.mu.Unlock()
	for _, s := range all {
		s.mu.Lock()
		p.end(s)
		s.mu.Unlock()
	}
}

// Running is how many browsers the pool holds open.
func (p *Pool) Running() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.people)
}

// errClosedPool says the server is closing.
var errClosedPool = errors.New("the server is closing, so the browser was not used")

// with runs do with the person's browser, starting one if there is none,
// and holds it for the person alone meanwhile. The idle time starts again
// after.
func (p *Pool) with(ctx context.Context, s *tenant.Store, do func(*person) error) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errClosedPool
	}
	sess := p.people[s.TenantID()]
	if sess == nil {
		sess = &person{tenantID: s.TenantID()}
		p.people[s.TenantID()] = sess
	}
	p.mu.Unlock()

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.gone {
		// Closed while this call waited: start afresh.
		return p.with(ctx, s, do)
	}
	if sess.idle != nil {
		sess.idle.Stop()
	}
	if sess.b == nil {
		if err := p.start(ctx, s, sess); err != nil {
			p.forget(sess)
			return err
		}
	}
	err := do(sess)
	if sess.b != nil {
		sess.idle = time.AfterFunc(p.o.IdleTimeout, func() { p.idleClose(sess) })
	}
	return err
}

func (p *Pool) forget(sess *person) {
	sess.gone = true
	p.mu.Lock()
	if p.people[sess.tenantID] == sess {
		delete(p.people, sess.tenantID)
	}
	p.mu.Unlock()
}

func (p *Pool) idleClose(sess *person) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	p.end(sess)
}

// end closes a person's browser and removes its profile. The caller holds
// sess.mu.
func (p *Pool) end(sess *person) {
	if sess.idle != nil {
		sess.idle.Stop()
	}
	if sess.b != nil {
		sess.b.Close()
		sess.b = nil
	}
	if sess.profile != "" {
		removeProfile(sess.profile)
		sess.profile = ""
	}
	sess.owner = ""
	p.forget(sess)
}

// removeProfile removes a profile folder, trying again for a moment while
// the browser's last files are let go.
func removeProfile(dir string) {
	for i := 0; i < 20; i++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// tenantFolder is the person's own folder, the parent of their uploads.
func tenantFolder(s *tenant.Store) (string, error) {
	if s == nil || s.Files() == nil {
		return "", errors.New("the person's folder is not known")
	}
	return filepath.Abs(filepath.Dir(s.Files().Name()))
}

// start starts the person's browser, with a fresh empty profile in their
// own folder, and proves it holds no cookies.
func (p *Pool) start(ctx context.Context, s *tenant.Store, sess *person) error {
	driver := p.o.Driver
	program := ""
	if driver == nil {
		var err error
		if program, err = FindProgram(p.o.Program); err != nil {
			return err
		}
		driver = Chromium{}
	}
	folder, err := tenantFolder(s)
	if err != nil {
		return err
	}
	profile := guard.Profile(folder)
	removeProfile(profile) // what a browser that was killed left
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return fmt.Errorf("the browser's profile folder could not be made: %w", err)
	}
	entries, err := os.ReadDir(profile)
	if err != nil {
		return err
	}
	if err := guard.CheckProfile(profile, folder, len(entries)); err != nil {
		return err
	}
	b, err := driver.Start(ctx, Launch{Program: program, Profile: profile, Width: p.o.Width, Height: p.o.Height, Policy: p.policy})
	if err != nil {
		removeProfile(profile)
		return err
	}
	cookies, err := b.Cookies(ctx)
	if err == nil {
		err = guard.CheckCookies(cookies, nil)
	}
	if err != nil {
		b.Close()
		removeProfile(profile)
		return fmt.Errorf("the browser was closed as it started: %w", err)
	}
	sess.b, sess.profile, sess.owner = b, profile, ""
	go func() {
		// A browser that stops by itself is forgotten, and the next call
		// starts a fresh one.
		<-b.Done()
		sess.mu.Lock()
		defer sess.mu.Unlock()
		if sess.b == b {
			p.end(sess)
		}
	}()
	return nil
}

// policy decides each request a browser is about to make: the guard's
// rule, then the host's addresses.
func (p *Pool) policy(ctx context.Context, address, method string, sending bool) error {
	if err := guard.CheckRequest(guard.Request{URL: address, Method: method, Sending: sending}, p.o.Blocked); err != nil {
		return err
	}
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil // the browser's own, which the guard let through
	}
	return p.hostAllowed(ctx, u.Hostname())
}

func (p *Pool) hostAllowed(ctx context.Context, host string) error {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if p.o.HostAllowed != nil {
		return p.o.HostAllowed(ctx, host)
	}
	p.mu.Lock()
	v, ok := p.hosts[host]
	p.mu.Unlock()
	if ok && time.Since(v.at) < hostCache {
		return v.err
	}
	err := publicHost(ctx, host)
	p.mu.Lock()
	if len(p.hosts) > 4096 {
		p.hosts = map[string]hostVerdict{}
	}
	p.hosts[host] = hostVerdict{err: err, at: time.Now()}
	p.mu.Unlock()
	return err
}

// publicHost is web_fetch's rule for a host: not a name that means this
// machine or a home network, and every address it resolves to on the
// public internet.
func publicHost(ctx context.Context, host string) error {
	if a, err := netip.ParseAddr(host); err == nil {
		if !web.IsPublic(a.Unmap()) {
			return fmt.Errorf("%s is not on the public internet, and the browser reads only public pages", host)
		}
		return nil
	}
	if web.IsLocalName(host) {
		return fmt.Errorf("%s names this machine or a home network, and the browser reads only public pages", host)
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("%s could not be found", host)
	}
	for _, a := range addrs {
		if !web.IsPublic(a.Unmap()) {
			return fmt.Errorf("%s points at an address that is not on the public internet, and the browser reads only public pages", host)
		}
	}
	return nil
}
