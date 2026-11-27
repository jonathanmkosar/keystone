package browser

import (
	"context"
	"errors"

	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
)

// Driver starts browsers. Chromium, which drives a real Chromium-family
// browser over the DevTools pipe, is the one Keystone ships; Fake keeps
// pages in memory, for tests and for verify on a machine with no browser.
type Driver interface {
	// Start starts one browser with the launch given. The browser asks
	// launch.Policy before every request it makes, and makes none it
	// refuses.
	Start(ctx context.Context, launch Launch) (Browser, error)
}

// Launch is what one browser is started with.
type Launch struct {
	// Program is the browser's program, for a driver that runs one.
	Program string
	// Profile is the profile folder: fresh, empty, and inside the person's
	// own tenant folder (guard.CheckProfile has passed it).
	Profile string
	// Width and Height are the window's size, and so a screenshot's.
	Width, Height int
	// Policy decides each request the browser is about to make. sending
	// says a click or a typing the person approved is under way.
	Policy func(ctx context.Context, url, method string, sending bool) error
}

// Browser is one running browser, with one tab.
type Browser interface {
	// Navigate opens an address in the tab and waits for it to load.
	Navigate(ctx context.Context, address string) error
	// Page reads the tab: its address, title and text, and its elements,
	// numbered, at most maxElements of them.
	Page(ctx context.Context, maxElements int) (Page, error)
	// Element describes one numbered element, as the last Page numbered it.
	Element(ctx context.Context, ref int) (guard.Element, error)
	// Click clicks a numbered element and waits for what it sets off.
	Click(ctx context.Context, ref int) error
	// Type puts text into a numbered element, after check passes the
	// element the focus is on. It presses no key: a form is sent by a
	// click.
	Type(ctx context.Context, ref int, text string, check func(guard.Element) error) error
	// Back goes back one page in the tab's history.
	Back(ctx context.Context) error
	// Screenshot takes a JPEG of what the tab shows.
	Screenshot(ctx context.Context) ([]byte, error)
	// Cookies are every cookie the profile holds.
	Cookies(ctx context.Context) ([]guard.Cookie, error)
	// Asked are the hosts the browser has asked for since it started.
	Asked() []string
	// SetSending says whether a click or a typing the person approved is
	// under way: only then may a request send anything.
	SetSending(on bool)
	// Notes takes what the browser refused or dismissed since the last
	// call, in sentences the model reads.
	Notes() []string
	// Done is closed when the browser has stopped.
	Done() <-chan struct{}
	// Close stops the browser and waits for it.
	Close() error
}

// Page is what a tab shows.
type Page struct {
	URL      string
	Title    string
	Text     string
	Elements []Element
}

// Element is one numbered element of a page: something to click or type
// into.
type Element struct {
	Ref   int
	Kind  string // guard.Kind of it: "link", "button", "text field", "password field"…
	Label string
	Href  string
}

// ErrNoBrowser says no browser program was found on this machine.
var ErrNoBrowser = errors.New("no Chromium-family browser was found on this machine: install Microsoft Edge or Google Chrome, or name one in agents.browser_path")

// ErrNoElement says a page has no element of the number asked for.
var ErrNoElement = errors.New("the page has no element of that number now: it may have changed. Read it again with browser_read")
