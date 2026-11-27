// Package guard is the rule of browser use (P10-03): what the agent's own
// browser may do, decided from plain values.
//
// It is a guard of fixed rule 2, beside internal/tools/approval: clicking
// and typing are side effects, so the tools that do them are never
// read-only and ask under the approval policy, and always once a chat has
// read a page; and a page cannot send anything on its own, because every
// request but GET, HEAD and OPTIONS is refused unless a click or a typing
// the person approved is under way. It also holds the rules that keep the
// person's own sign-ins out of reach: no typing into a password, payment,
// one-time-code or sign-in field, no button of a sign-in form, a fresh and
// empty profile inside the person's own tenant folder, a browser driven
// over a pipe only this process holds, and no cookie but those of the
// hosts this browser itself asked for.
//
// It imports nothing of Keystone's, so it can read no setting: the block
// list comes in as a value. The fixed rules check in verify fails if that
// ever changes. docs/architecture/browser-use.md is the whole of it.
package guard

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// Action is one thing the agent asks its browser to do.
type Action string

// The actions, one for each tool.
const (
	Open       Action = "open"
	Read       Action = "read"
	Click      Action = "click"
	Type       Action = "type"
	Screenshot Action = "screenshot"
	Back       Action = "back"
)

// Actions are every action, in the order the tools are listed.
var Actions = []Action{Open, Read, Click, Type, Screenshot, Back}

// SideEffect reports whether an action may change something outside
// Keystone: a click may press a button that sends a form, buys or posts,
// and typing puts the agent's words into a page that may send them. Such a
// tool is never read-only, so the approval policy asks for it, and always
// once the chat has read a page. Opening a page, reading it, a screenshot
// and going back only read, as web_fetch does. An action this package does
// not know is a side effect.
func SideEffect(a Action) bool {
	switch a {
	case Open, Read, Screenshot, Back:
		return false
	}
	return true
}

// maxAddress is the most characters of an address, a bound that is part of
// the rule's shape, not a choice an owner makes.
const maxAddress = 2048

// schemeOnly matches an address that starts with a scheme and a colon.
var schemeOnly = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*):`)

// ParseList reads a list of hosts as a setting holds it: separated by
// commas, spaces or new lines. Each is put in lower case, with a leading
// "*." or "." and a trailing dot taken off. Empty entries are dropped.
func ParseList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == ';' }) {
		if h := cleanHost(f); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func cleanHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(h, "*.")
	h = strings.TrimPrefix(h, ".")
	return strings.TrimSuffix(h, ".")
}

// Blocked reports whether a host is on the block list: the host itself, or
// any name under it. "bank.example" blocks "bank.example" and
// "www.bank.example", never "notbank.example".
func Blocked(host string, blocked []string) bool {
	h := cleanHost(strings.Trim(host, "[]"))
	if h == "" {
		return false
	}
	for _, b := range blocked {
		b = cleanHost(b)
		if b != "" && (h == b || strings.HasSuffix(h, "."+b)) {
			return true
		}
	}
	return false
}

// CheckAddress reads the address of a page the agent asks to open. It must
// be http or https, with a host, no user name or password, and a host not
// on the block list. An address with no scheme is read as https. Whether
// the host is on the public internet is checked by the browser's request
// check as it connects, with web_fetch's rules.
func CheckAddress(raw string, blocked []string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return nil, errors.New("there is no address to open")
	case len(raw) > maxAddress:
		return nil, fmt.Errorf("the address is longer than %d characters, so it was not opened", maxAddress)
	}
	if !strings.Contains(raw, "://") {
		// "javascript:…", "file:…" and "data:…" keep their scheme, and are
		// refused below; "harbour.example" and "harbour.example:8080/x" are
		// read as https.
		if m := schemeOnly.FindStringSubmatch(raw); m == nil || strings.Contains(m[1], ".") {
			raw = "https://" + raw
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("the address does not read as a web address, so it was not opened")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, fmt.Errorf("only http and https addresses can be opened, and this one is %s:", u.Scheme)
	case u.Hostname() == "":
		return nil, errors.New("the address has no host, so it was not opened")
	case u.User != nil:
		return nil, errors.New("the address holds a user name or a password, and the browser never signs in, so it was not opened")
	case Blocked(u.Hostname(), blocked):
		return nil, fmt.Errorf("%s is on the list of sites the browser does not open (agents.browser_blocked_hosts)", u.Hostname())
	}
	return u, nil
}

// Request is one request the browser is about to make, for a page, a frame,
// a script or anything else a page asks for.
type Request struct {
	URL    string
	Method string
	// Sending says a click or a typing that the person approved is under
	// way, so a form it sends may go.
	Sending bool
}

// Kinds of browser address that never leave the machine: the browser's own
// pages and the parts of itself it loads. A page cannot open them.
var ownSchemes = map[string]bool{"chrome-extension": true, "chrome": true, "edge": true, "devtools": true, "about": true, "data": true, "blob": true}

// readMethods are the methods that only ask for something.
var readMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true}

// ErrSends is the reason a request that would send something is refused:
// no click or typing the person approved is under way.
var ErrSends = errors.New("the page tried to send something, and only a click or typing the person approved may send")

// CheckRequest decides one request of the browser. A request that only
// asks (GET, HEAD, OPTIONS) goes when its address is http or https and its
// host is not blocked, or when it is one of the browser's own. Any other
// method, which sends a form or data, goes only while Sending: so a page
// that sends its form as it loads, or a script that posts in the
// background, sends nothing. Anything else is refused.
func CheckRequest(r Request, blocked []string) error {
	u, err := url.Parse(r.URL)
	if err != nil {
		return errors.New("the request's address does not read")
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		method = "GET"
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case ownSchemes[scheme]:
		if !readMethods[method] {
			return ErrSends
		}
		return nil
	case scheme != "http" && scheme != "https":
		return fmt.Errorf("the browser asks only for http and https addresses, and this request is %s:", scheme)
	case u.User != nil:
		return errors.New("the request holds a user name or a password, and the browser never signs in")
	case Blocked(u.Hostname(), blocked):
		return fmt.Errorf("%s is on the list of sites the browser does not open", u.Hostname())
	case !readMethods[method] && !r.Sending:
		return ErrSends
	}
	return nil
}

// Element is one element of a page, as the browser describes it before the
// agent clicks it or types into it.
type Element struct {
	// Tag is the element's tag name, in lower case: input, textarea,
	// select, button, a.
	Tag string
	// Type is an input's or a button's type attribute, in lower case.
	Type string
	// Role is the element's ARIA role, if it has one.
	Role string
	// Name, ID, Autocomplete, InputMode, Label and Placeholder are what
	// the page says about it. Label is its visible or ARIA label.
	Name, ID, Autocomplete, InputMode, Label, Placeholder string
	// Editable says text can be typed into it.
	Editable bool
	// InPasswordForm says its form, or the page when it has no form,
	// holds a password field: it is part of a sign-in.
	InPasswordForm bool
	// Href is a link's address, and FormAction the address its form
	// sends to.
	Href, FormAction string
}

var (
	// payment matches what a payment field calls itself.
	payment = regexp.MustCompile(`(?i)(card.?(number|num|no\b|holder|verification|security)|credit.?card|debit.?card|cc.?(num|number|csc|cvv|exp)|\bcvv|\bcvc|\bcsc\b|security.?code|expir|exp.?(date|month|year)|\biban\b|routing|sort.?code|account.?(number|num|no\b)|bank.?account|swift|\bbic\b)`)
	// secret matches what a password or a one-time-code field calls itself.
	secret = regexp.MustCompile(`(?i)(passw|passcode|pass.?code|pass.?phrase|\bpin\b|\botp\b|one.?time|verification.?code|auth.?code|2fa|\bmfa\b|security.?answer|secret)`)
	// signin matches the name of a field that holds a user name for a
	// sign-in.
	signin = regexp.MustCompile(`(?i)^(user.?name|login|log.?in.?(id|name|email)?|sign.?in.?(id|name|email)?|j_username|identifier|loginfmt)$`)
	// credential matches text that looks like it holds a key, a token or a
	// password, as web_search refuses such queries.
	credential = regexp.MustCompile(`(?i)(tvly-|sk-[a-z0-9]|sk-proj-|sk-ant-|-----BEGIN|\bbearer\s+[a-z0-9]|\bapi[_ -]?key\s*[:=]|\bpassword\s*[:=]|\btoken\s*[:=]|\bsecret\s*[:=]|\bgh[pousr]_[a-z0-9]{20,}|\bAKIA[0-9A-Z]{16})`)
)

// autocomplete tokens and what they mean.
func autocompleteKind(ac string) string {
	for _, tok := range strings.Fields(strings.ToLower(ac)) {
		switch {
		case tok == "current-password" || tok == "new-password":
			return "password"
		case tok == "one-time-code":
			return "one-time code"
		case strings.HasPrefix(tok, "cc-") || tok == "transaction-amount":
			return "payment"
		case tok == "username" || tok == "webauthn":
			return "sign-in"
		}
	}
	return ""
}

// Kind says what an element is, in a few words the model and the person
// read: "password field", "payment field", "sign-in field", "text field",
// "button", "link" and the like.
func Kind(e Element) string {
	tag, typ := strings.ToLower(e.Tag), strings.ToLower(e.Type)
	words := e.Name + " " + e.ID + " " + e.Label + " " + e.Placeholder
	switch {
	case tag == "input" && typ == "password":
		return "password field"
	case tag == "input" && typ == "file":
		return "file chooser"
	}
	if e.Editable || tag == "input" || tag == "textarea" || tag == "select" {
		switch autocompleteKind(e.Autocomplete) {
		case "password":
			return "password field"
		case "one-time code":
			return "one-time code field"
		case "payment":
			return "payment field"
		case "sign-in":
			return "sign-in field"
		}
		if e.Editable {
			switch {
			case payment.MatchString(words):
				return "payment field"
			case secret.MatchString(words):
				return "password field"
			case signin.MatchString(strings.TrimSpace(e.Name)) || signin.MatchString(strings.TrimSpace(e.ID)):
				return "sign-in field"
			case e.InPasswordForm:
				return "sign-in field"
			}
		}
	}
	switch {
	case tag == "a":
		return "link"
	case tag == "button" || typ == "submit" || typ == "button" || typ == "image" || typ == "reset" || e.Role == "button":
		return "button"
	case tag == "select":
		return "list"
	case typ == "checkbox" || e.Role == "checkbox":
		return "checkbox"
	case typ == "radio" || e.Role == "radio":
		return "choice"
	case e.Editable:
		return "text field"
	}
	return "element"
}

// Refused says whether the browser never types into an element of this
// kind.
func Refused(kind string) bool {
	switch kind {
	case "password field", "payment field", "one-time code field", "sign-in field", "file chooser":
		return true
	}
	return false
}

// CheckType decides whether the agent may type text into an element. It
// never types into a password, payment, one-time-code or sign-in field, a
// field of a form that holds a password, or a file chooser, and never types
// text that looks like a key, a token or a password: the browser never
// signs in and never pays.
func CheckType(e Element, text string) error {
	kind := Kind(e)
	switch {
	case kind == "password field":
		return errors.New("that is a password field, and the browser never types into one: it never signs in")
	case kind == "payment field":
		return errors.New("that is a payment field, and the browser never types into one: it never pays")
	case kind == "one-time code field":
		return errors.New("that is a field for a one-time code, and the browser never types into one: it never signs in")
	case kind == "sign-in field":
		return errors.New("that field is part of a sign-in, and the browser never signs in")
	case kind == "file chooser":
		return errors.New("that is a file chooser, and the browser never sends a file")
	case !e.Editable:
		return fmt.Errorf("that is a %s, and text cannot be typed into it", kind)
	case strings.TrimSpace(text) == "":
		return errors.New("there is no text to type")
	case credential.MatchString(text):
		return errors.New("the text looks like it holds a key, a token or a password, so it was not typed")
	}
	return nil
}

// CheckClick decides whether the agent may click an element. It never
// clicks a file chooser, nor a button of a form that holds a password
// field (that would sign in), nor a link or a form that leads to a blocked
// host.
func CheckClick(e Element, blocked []string) error {
	kind := Kind(e)
	switch {
	case kind == "file chooser":
		return errors.New("that is a file chooser, and the browser never sends a file")
	case Refused(kind):
		return fmt.Errorf("that is a %s; the browser never signs in or pays", kind)
	case kind == "button" && e.InPasswordForm:
		return errors.New("that button sends a sign-in form, and the browser never signs in")
	}
	for _, addr := range []string{e.Href, e.FormAction} {
		if u, err := url.Parse(strings.TrimSpace(addr)); err == nil && (u.Scheme == "http" || u.Scheme == "https") && Blocked(u.Hostname(), blocked) {
			return fmt.Errorf("that leads to %s, which is on the list of sites the browser does not open", u.Hostname())
		}
	}
	return nil
}

// Cookie is one cookie the profile holds, by its name and domain.
type Cookie struct {
	Name   string
	Domain string
}

// CheckCookies proves the profile holds no cookie but its own: each one
// must be for a host this browser itself asked for since it started (a
// cookie's domain may be the host or a parent domain of it). A browser that
// asked for nothing holds none. A cookie from anywhere else, such as one of
// the person's own browsers, fails the check.
func CheckCookies(cookies []Cookie, asked []string) error {
	for _, c := range cookies {
		d := cleanHost(c.Domain)
		ok := false
		for _, h := range asked {
			h = cleanHost(h)
			if h != "" && d != "" && (h == d || strings.HasSuffix(h, "."+d)) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("the browser's profile holds a cookie for %s, a site it never asked for, so it is not the agent's own", strings.TrimPrefix(c.Domain, "."))
		}
	}
	return nil
}

// ProfileFolder is the name of the folder, inside a person's tenant
// folder, that holds the agent's browser profile while the browser runs.
const ProfileFolder = "browser-profile"

// Profile returns the profile folder of the tenant whose folder is
// tenantDir.
func Profile(tenantDir string) string { return filepath.Join(tenantDir, ProfileFolder) }

// CheckProfile proves a profile folder may be used: it is the profile
// folder inside the person's own tenant folder, and it is empty, so the
// browser starts with nothing in it, none of the person's sign-ins and
// none of an earlier start's. entries is how many files the folder holds.
func CheckProfile(profile, tenantDir string, entries int) error {
	want, err := filepath.Abs(Profile(tenantDir))
	if err != nil {
		return err
	}
	got, err := filepath.Abs(profile)
	if err != nil {
		return err
	}
	switch {
	case tenantDir == "" || !strings.EqualFold(filepath.Clean(got), filepath.Clean(want)):
		return fmt.Errorf("the browser's profile must be %s, inside the person's own folder", want)
	case entries != 0:
		return errors.New("the browser's profile folder is not empty, and the agent's browser starts with an empty one")
	}
	return nil
}

// Args returns the command line the browser is started with: headless,
// with the profile given and nothing of the machine's own browsers, driven
// over the DevTools pipe (pipeArgs names the pipe's two ends, as the
// operating system passes them), with sync, sign-in, extensions,
// background networking and the machine's proxy off, at width by height.
func Args(profile string, width, height int, pipeArgs ...string) []string {
	args := []string{
		"--headless=new",
		"--user-data-dir=" + profile,
		"--remote-debugging-pipe",
	}
	args = append(args, pipeArgs...)
	return append(args,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-extensions",
		"--disable-features=msImplicitSignin,msEdgeSignin,AutofillServerCommunication,MediaRouter",
		"--no-proxy-server",
		"--password-store=basic",
		"--use-mock-keychain",
		"--mute-audio",
		"--hide-scrollbars",
		fmt.Sprintf("--window-size=%d,%d", width, height),
		"about:blank",
	)
}

// forbiddenArgs are the switches that would open the browser to anything
// but this process, use another profile, or load something of the
// machine's.
var forbiddenArgs = []string{"--remote-debugging-port", "--remote-debugging-address", "--remote-allow-origins", "--profile-directory", "--load-extension", "--proxy-server", "--proxy-pac-url", "--user-agent"}

// CheckArgs proves a command line is one the agent's browser may start
// with: headless, the DevTools pipe and no port or address, the profile
// folder given (and no other), and nothing on the forbidden list.
func CheckArgs(args []string, profile string) error {
	var headless, pipe bool
	profiles := 0
	for _, a := range args {
		name, value, _ := strings.Cut(a, "=")
		for _, bad := range forbiddenArgs {
			if strings.EqualFold(name, bad) {
				return fmt.Errorf("the browser may not start with %s", bad)
			}
		}
		switch strings.ToLower(name) {
		case "--headless":
			headless = value == "new" || value == ""
		case "--remote-debugging-pipe":
			pipe = true
		case "--user-data-dir":
			profiles++
			if filepath.Clean(value) != filepath.Clean(profile) {
				return errors.New("the browser may start only with the agent's own profile")
			}
		}
	}
	switch {
	case !headless:
		return errors.New("the browser starts headless")
	case !pipe:
		return errors.New("the browser is driven over its DevTools pipe only")
	case profiles != 1:
		return errors.New("the browser starts with exactly one profile, the agent's own")
	}
	return nil
}
