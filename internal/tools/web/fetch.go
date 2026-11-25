package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// The fetcher reads one public web page. It must never be a way into the
// machine Keystone runs on or the network around it, so every address it
// connects to is checked as it connects: after the name is resolved, and
// again for each redirect, which is dialled anew. A name that resolves to
// a private address is refused, whatever it is called.

// notPublicRanges are the ranges outside the ones the standard library
// names (loopback, private, link-local, multicast, unspecified) that are
// not the public internet, or that can carry an address that is not.
var notPublicRanges = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"0.0.0.0/8",       // "this network"
		"100.64.0.0/10",   // shared address space: carrier NAT, and the range Tailscale uses
		"192.0.0.0/24",    // protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved, and the broadcast address
		"64:ff9b::/96",    // NAT64, which can carry a private IPv4 address
		"64:ff9b:1::/48",  // local NAT64
		"100::/64",        // discard
		"2001::/32",       // Teredo, which carries an IPv4 address
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4, which carries an IPv4 address
		"fec0::/10",       // the old site-local range
		"ff00::/8",        // multicast
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

// public reports whether an address is on the public internet: not this
// machine, not a private or home network, not link-local, and not a
// reserved or special range.
func public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range notPublicRanges {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// localName reports whether a host name is one that only ever means this
// machine or the local network.
func localName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	for _, end := range []string{".localhost", ".local", ".lan", ".home", ".internal", ".intranet", ".corp", ".home.arpa"} {
		if strings.HasSuffix(h, end) {
			return true
		}
	}
	return false
}

// errNotPublic is the refusal of an address that is not public.
type errNotPublic struct{ host string }

func (e errNotPublic) Error() string { return e.host + " is not a public address" }

// said is a refusal already written for the model, which is passed on as
// it is, even from inside a redirect.
type said string

func (s said) Error() string { return string(s) }

// page is a fetched page.
type page struct {
	url       string // the address it was read from, after any redirect
	title     string
	text      string
	cutBytes  bool // the page was longer than the bytes Keystone reads
	cutChars  int  // when the text was cut, how many characters it had
	keptChars int
}

// fetcher reads pages.
type fetcher struct {
	timeout   time.Duration
	maxBytes  int64
	maxChars  int
	redirects int
	// allow says whether an address may be connected to. It is public,
	// except in this package's tests, which serve pages on this machine.
	allow func(netip.Addr) bool
	// lookup resolves a host name. Nil means the system's resolver. The
	// tests give names their own addresses.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
}

// dial connects to a checked address. Every address the name resolves to
// must be allowed, so a name that points both at the internet and at the
// home network is refused. The address dialled is the one checked, and
// the socket checks it once more as it connects.
func (f fetcher) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		lookup := f.lookup
		if lookup == nil {
			lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
				return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			}
		}
		if addrs, err = lookup(ctx, host); err != nil {
			return nil, err
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s has no address", host)
	}
	for _, a := range addrs {
		if !f.allow(a) {
			return nil, errNotPublic{host}
		}
	}
	d := net.Dialer{
		Timeout: f.timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !f.allow(ap.Addr()) {
				return errNotPublic{host}
			}
			return nil
		},
	}
	var last error
	for _, a := range addrs {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

// checkAddress refuses an address web_fetch will not read, before any
// connection: not http or https, a user name in it, no host, a name that
// only means the local network, or an address that is not public.
func (f fetcher) checkAddress(u *url.URL) error {
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return said(fmt.Sprintf("web_fetch reads only http and https addresses, and %q is neither", u.String()))
	case u.User != nil:
		return said("web_fetch does not read an address with a user name or a password in it")
	case u.Hostname() == "":
		return said(fmt.Sprintf("%q has no host, so web_fetch cannot read it", u.String()))
	}
	if a, err := netip.ParseAddr(u.Hostname()); err == nil {
		if !f.allow(a) {
			return errNotPublic{u.Hostname()}
		}
		return nil
	}
	if localName(u.Hostname()) && !f.allow(netip.MustParseAddr("127.0.0.1")) {
		return errNotPublic{u.Hostname()}
	}
	return nil
}

func (f fetcher) client() *http.Client {
	transport := &http.Transport{
		// The environment's proxy is never used: the check must see the
		// address really connected to, and a proxy's would hide it.
		Proxy:                  nil,
		DialContext:            f.dial,
		ForceAttemptHTTP2:      true,
		TLSHandshakeTimeout:    f.timeout,
		ResponseHeaderTimeout:  f.timeout,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   f.timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > f.redirects {
				return said(fmt.Sprintf("the page redirected more than %d times (agents.web_fetch_max_redirects), so it was not read", f.redirects))
			}
			return f.checkAddress(req.URL)
		},
	}
}

// fetch reads the page at address and returns its readable text.
func (f fetcher) fetch(ctx context.Context, address string) (page, error) {
	u, err := url.Parse(address)
	if err != nil {
		return page{}, fmt.Errorf("%q is not an address web_fetch can read", address)
	}
	if err := f.checkAddress(u); err != nil {
		return page{}, refusal(address, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return page{}, fmt.Errorf("%q is not an address web_fetch can read", address)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain;q=0.9, */*;q=0.1")
	resp, err := f.client().Do(req)
	if err != nil {
		return page{}, refusal(address, err)
	}
	defer resp.Body.Close()
	final := resp.Request.URL.String()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return page{}, fmt.Errorf("the page at %s answered with status %d, so it has no text to read", final, resp.StatusCode)
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	html := kind == "text/html" || kind == "application/xhtml+xml"
	text := html || kind == "" || strings.HasPrefix(kind, "text/") || kind == "application/json" || kind == "application/xml" || strings.HasSuffix(kind, "+json") || strings.HasSuffix(kind, "+xml")
	if !text {
		return page{}, fmt.Errorf("the page at %s is a %s file, and web_fetch reads only web pages and text", final, kind)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil && len(raw) == 0 {
		if ctx.Err() != nil {
			return page{}, ctx.Err()
		}
		return page{}, fmt.Errorf("the page at %s could not be read", final)
	}
	// A page that broke off part of the way is read as far as it came.
	p := page{url: final, cutBytes: err != nil}
	if int64(len(raw)) > f.maxBytes {
		raw, p.cutBytes = raw[:f.maxBytes], true
	}
	body := string(raw)
	if !utf8.ValidString(body) {
		body = strings.ToValidUTF8(body, "")
	}
	if html {
		p.title, p.text = readable(body)
	} else {
		p.text = plain(body)
	}
	if runes := []rune(p.text); f.maxChars > 0 && len(runes) > f.maxChars {
		p.cutChars, p.keptChars = len(runes), f.maxChars
		p.text = string(runes[:f.maxChars])
	}
	return p, nil
}

// refusal turns a fetch's error into a sentence. An address that is not
// public is named as such; the rest say what failed, without the details
// of the network.
func refusal(address string, err error) error {
	var np errNotPublic
	var s said
	var dns *net.DNSError
	switch {
	case errors.As(err, &np):
		return fmt.Errorf("web_fetch reads only public pages, and %s leads to an address on this machine or a private network (%s), so it was not fetched", address, np.host)
	case errors.As(err, &s):
		return s
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return fmt.Errorf("the page at %s did not answer in time, so it was not read", address)
	case errors.As(err, &dns):
		return fmt.Errorf("the name in %s could not be found, so the page was not read", address)
	}
	return fmt.Errorf("the page at %s could not be reached, so it was not read", address)
}
