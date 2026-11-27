package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
)

// Fake is a driver whose browsers keep their pages in memory. Every request
// a page would make goes through the launch's policy, as the real
// browser's does, so the rules are proved with it on any machine, and
// verify uses it where no browser is installed. It is not a setting: a
// test or a check passes it in Options.Driver.
type Fake struct {
	// Pages are the pages it serves, by address.
	Pages map[string]FakePage
	// Planted are cookies its profile holds as it starts, to prove that a
	// profile holding a cookie it was not given is refused. Empty for a
	// fresh one.
	Planted []guard.Cookie

	mu       sync.Mutex
	sent     []string
	typed    []string
	launches []Launch
	open     int
}

// FakePage is one page of a fake browser.
type FakePage struct {
	Title string
	Text  string
	// Elements are its links, buttons and fields, numbered from 1.
	Elements []FakeElement
	// Cookies are the names of the cookies the page sets for its own host
	// as it loads.
	Cookies []string
	// SendsOnLoad is an address the page's script sends a form to as it
	// loads, with POST.
	SendsOnLoad string
	// Plants are cookies that appear in the profile as the page loads, for
	// any domain: what a real browser would never let a page do, to prove
	// that a profile holding a cookie it was not given is closed.
	Plants []guard.Cookie
}

// FakeElement is one element of a fake page.
type FakeElement struct {
	guard.Element
	// Goes is the address a click on it opens: a link.
	Goes string
	// Sends is the address a click on it sends its form to, with POST.
	Sends string
}

// Sent are the addresses a request that sends something reached, in order.
func (f *Fake) Sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// Typed is what was typed, as "ref: text", in order.
func (f *Fake) Typed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...)
}

// Launches are the launches its browsers were started with.
func (f *Fake) Launches() []Launch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Launch(nil), f.launches...)
}

// Open is how many of its browsers are running.
func (f *Fake) Open() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open
}

// Start starts one fake browser. Like the real driver, it is given a
// launch whose profile and command line the guard has passed.
func (f *Fake) Start(ctx context.Context, l Launch) (Browser, error) {
	if err := guard.CheckArgs(guard.Args(l.Profile, l.Width, l.Height), l.Profile); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.launches = append(f.launches, l)
	f.open++
	f.mu.Unlock()
	b := &fakeBrowser{f: f, l: l, at: -1, asked: map[string]bool{}, done: make(chan struct{})}
	b.cookies = append(b.cookies, f.Planted...)
	return b, nil
}

type fakeBrowser struct {
	f       *Fake
	l       Launch
	mu      sync.Mutex
	history []string
	at      int
	cookies []guard.Cookie
	asked   map[string]bool
	notes   []string
	sending bool
	closed  bool
	done    chan struct{}
}

func (b *fakeBrowser) current() (string, FakePage, bool) {
	if b.at < 0 || b.at >= len(b.history) {
		return "", FakePage{}, false
	}
	addr := b.history[b.at]
	p, ok := b.f.Pages[addr]
	return addr, p, ok
}

// request asks the policy, and records the host when it may go.
func (b *fakeBrowser) request(ctx context.Context, address, method string) error {
	b.mu.Lock()
	sending := b.sending
	b.mu.Unlock()
	if err := b.l.Policy(ctx, address, method, sending); err != nil {
		return err
	}
	if u, err := url.Parse(address); err == nil {
		b.mu.Lock()
		b.asked[strings.ToLower(u.Hostname())] = true
		b.mu.Unlock()
	}
	return nil
}

func (b *fakeBrowser) load(ctx context.Context, address string) error {
	if err := b.request(ctx, address, "GET"); err != nil {
		return upperErr(err)
	}
	p, ok := b.f.Pages[address]
	if !ok {
		return fmt.Errorf("the page could not be opened (nothing is served at %s)", address)
	}
	u, _ := url.Parse(address)
	b.mu.Lock()
	for _, name := range p.Cookies {
		b.cookies = append(b.cookies, guard.Cookie{Name: name, Domain: u.Hostname()})
	}
	b.cookies = append(b.cookies, p.Plants...)
	b.mu.Unlock()
	if p.SendsOnLoad != "" {
		b.send(ctx, p.SendsOnLoad)
	}
	return nil
}

// send is a form sent to address, by a click or by a page's own script.
func (b *fakeBrowser) send(ctx context.Context, address string) bool {
	if err := b.request(ctx, address, "POST"); err != nil {
		u, _ := url.Parse(address)
		host := address
		if u != nil {
			host = u.Host
		}
		if errors.Is(err, guard.ErrSends) {
			b.note(fmt.Sprintf("The page tried to send something to %s with POST, and Keystone stopped it: only a click or typing the person approved may send.", host))
		} else {
			b.note(fmt.Sprintf("Keystone stopped the browser loading %s: %s.", host, err))
		}
		return false
	}
	b.f.mu.Lock()
	b.f.sent = append(b.f.sent, address)
	b.f.mu.Unlock()
	return true
}

func (b *fakeBrowser) note(s string) {
	b.mu.Lock()
	b.notes = append(b.notes, s)
	b.mu.Unlock()
}

func (b *fakeBrowser) Navigate(ctx context.Context, address string) error {
	if err := b.load(ctx, address); err != nil {
		return err
	}
	b.mu.Lock()
	b.history = append(b.history[:b.at+1], address)
	b.at = len(b.history) - 1
	b.mu.Unlock()
	return nil
}

func (b *fakeBrowser) Page(ctx context.Context, maxElements int) (Page, error) {
	addr, p, ok := b.current()
	if !ok {
		return Page{URL: "about:blank"}, nil
	}
	out := Page{URL: addr, Title: p.Title, Text: p.Text}
	for i, e := range p.Elements {
		if i >= maxElements {
			break
		}
		out.Elements = append(out.Elements, Element{Ref: i + 1, Kind: guard.Kind(e.Element), Label: e.Label, Href: e.Goes})
	}
	return out, nil
}

func (b *fakeBrowser) element(ref int) (FakeElement, error) {
	_, p, ok := b.current()
	if !ok || ref < 1 || ref > len(p.Elements) {
		return FakeElement{}, ErrNoElement
	}
	return p.Elements[ref-1], nil
}

func (b *fakeBrowser) Element(ctx context.Context, ref int) (guard.Element, error) {
	e, err := b.element(ref)
	return e.Element, err
}

func (b *fakeBrowser) Click(ctx context.Context, ref int) error {
	e, err := b.element(ref)
	if err != nil {
		return err
	}
	switch {
	case e.Sends != "":
		if b.send(ctx, e.Sends) {
			if _, ok := b.f.Pages[e.Sends]; ok {
				return b.Navigate(ctx, e.Sends)
			}
		}
	case e.Goes != "":
		return b.Navigate(ctx, e.Goes)
	}
	return nil
}

func (b *fakeBrowser) Type(ctx context.Context, ref int, text string, check func(guard.Element) error) error {
	e, err := b.element(ref)
	if err != nil {
		return err
	}
	if err := check(e.Element); err != nil {
		return err
	}
	b.f.mu.Lock()
	b.f.typed = append(b.f.typed, fmt.Sprintf("%d: %s", ref, text))
	b.f.mu.Unlock()
	return nil
}

func (b *fakeBrowser) Back(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.at < 1 {
		return errors.New("there is no page to go back to")
	}
	b.at--
	return nil
}

func (b *fakeBrowser) Screenshot(ctx context.Context) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 10))
	for x := 0; x < 16; x++ {
		for y := 0; y < 10; y++ {
			img.Set(x, y, color.RGBA{R: 0xb8, G: 0x73, B: 0x33, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (b *fakeBrowser) Cookies(ctx context.Context) ([]guard.Cookie, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]guard.Cookie(nil), b.cookies...), nil
}

func (b *fakeBrowser) Asked() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for h := range b.asked {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func (b *fakeBrowser) SetSending(on bool) {
	b.mu.Lock()
	b.sending = on
	b.mu.Unlock()
}

func (b *fakeBrowser) Notes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := dedupe(b.notes)
	b.notes = nil
	return out
}

func (b *fakeBrowser) Done() <-chan struct{} { return b.done }

func (b *fakeBrowser) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.done)
		b.f.mu.Lock()
		b.f.open--
		b.f.mu.Unlock()
	}
	return nil
}

func upperErr(err error) error { return errors.New(upperFirst(err.Error())) }
