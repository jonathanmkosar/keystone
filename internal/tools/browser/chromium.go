package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tools/browser/guard"
)

// Chromium is the driver Keystone ships: a Chromium-family browser already
// on the machine, started headless with an empty profile and driven over
// its DevTools pipe (decision 0580). Every request the browser makes,
// for any tab, frame or worker, is paused at the browser and goes only when
// the launch's policy lets it (the Fetch domain, enabled on the browser
// itself). Downloads are denied, dialogs dismissed, new windows closed, a
// file chooser never shown, and WebAuthn answered by an empty virtual
// authenticator, so the machine's own passkeys are never offered.
type Chromium struct{}

// Start starts one browser.
func (Chromium) Start(ctx context.Context, l Launch) (Browser, error) {
	if l.Program == "" {
		return nil, ErrNoBrowser
	}
	p, err := startProcess(l.Program, l)
	if err != nil {
		return nil, err
	}
	b := &chromium{p: p, l: l, asked: map[string]bool{}, done: make(chan struct{})}
	b.c = newConn(p.out, p.in, b.event)
	go func() {
		select {
		case <-p.wait:
		case <-b.c.done:
		}
		close(b.done)
	}()
	if err := b.setUp(ctx); err != nil {
		b.Close()
		return nil, fmt.Errorf("the browser did not start: %w", err)
	}
	return b, nil
}

type chromium struct {
	p       *process
	c       *conn
	l       Launch
	tab     string // the target id of the one tab
	session string // the tab's session
	sending atomic.Bool
	closed  atomic.Bool
	done    chan struct{}

	mu      sync.Mutex
	asked   map[string]bool
	notes   []string
	refused int    // requests refused since the last notes, besides those noted
	lastWhy string // why the last page request was refused
}

func (b *chromium) setUp(ctx context.Context) error {
	c := b.c
	// Every request is paused here first, before any page loads.
	if err := c.call(ctx, "", "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}, nil); err != nil {
		return err
	}
	if err := c.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "deny", "eventsEnabled": true}, nil); err != nil {
		return err
	}
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := c.call(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return err
	}
	for _, t := range targets.TargetInfos {
		if t.Type == "page" {
			b.tab = t.TargetID
			break
		}
	}
	if b.tab == "" {
		var made struct {
			TargetID string `json:"targetId"`
		}
		if err := c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &made); err != nil {
			return err
		}
		b.tab = made.TargetID
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": b.tab, "flatten": true}, &attached); err != nil {
		return err
	}
	b.session = attached.SessionID
	// Any other window a page opens is closed as it appears.
	if err := c.call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		return err
	}
	s := b.session
	for _, step := range []struct {
		method string
		params any
	}{
		{"Page.enable", nil},
		{"Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}},
		{"Emulation.setDeviceMetricsOverride", map[string]any{"width": b.l.Width, "height": b.l.Height, "deviceScaleFactor": 1, "mobile": false}},
	} {
		if err := c.call(ctx, s, step.method, step.params, nil); err != nil {
			return fmt.Errorf("%s: %w", step.method, err)
		}
	}
	// With the WebAuthn domain on and no virtual authenticator added, a
	// page that asks for a passkey is offered none of the machine's. A
	// browser without the domain is still headless, with an empty profile.
	_ = c.call(ctx, s, "WebAuthn.enable", map[string]any{"enableUI": false}, nil)
	return nil
}

// event handles what the browser sends of its own accord.
func (b *chromium) event(method, session string, params json.RawMessage) {
	switch method {
	case "Fetch.requestPaused":
		var p struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
			ResourceType string `json:"resourceType"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		sending := b.sending.Load()
		go b.decide(session, p.RequestID, p.Request.URL, p.Request.Method, p.ResourceType, sending)
	case "Page.javascriptDialogOpening":
		var p struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}
		json.Unmarshal(params, &p)
		go b.c.call(context.Background(), session, "Page.handleJavaScriptDialog", map[string]any{"accept": p.Type == "beforeunload"}, nil)
		if p.Type != "beforeunload" {
			b.note(fmt.Sprintf("The page showed a dialog (%q), and Keystone dismissed it.", clip(p.Message, 160)))
		}
	case "Page.fileChooserOpened":
		b.note("The page opened a file chooser, and Keystone left it unanswered: the browser never sends a file.")
	case "Browser.downloadWillBegin":
		b.note("The page started a download, and Keystone refused it: the browser never downloads.")
	case "Target.targetCreated":
		var p struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(params, &p)
		if p.TargetInfo.Type == "page" && p.TargetInfo.TargetID != b.tab && b.tab != "" {
			go b.c.call(context.Background(), "", "Target.closeTarget", map[string]any{"targetId": p.TargetInfo.TargetID}, nil)
			b.note("The page tried to open another window, and Keystone closed it. Open its address with browser_open instead.")
		}
	}
}

// decide lets one paused request go, or fails it, by the launch's policy.
func (b *chromium) decide(session, id, address, method, kind string, sending bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.l.Policy(ctx, address, method, sending); err != nil {
		b.mu.Lock()
		host := address
		if u, perr := url.Parse(address); perr == nil && u.Host != "" {
			host = u.Host
		}
		switch {
		case kind == "Document":
			b.lastWhy = upperFirst(err.Error())
			b.notes = append(b.notes, fmt.Sprintf("Keystone stopped the browser loading %s: %s.", host, strings.TrimSuffix(err.Error(), ".")))
		case errors.Is(err, guard.ErrSends):
			b.notes = append(b.notes, fmt.Sprintf("The page tried to send something to %s with %s, and Keystone stopped it: only a click or typing the person approved may send.", host, strings.ToUpper(method)))
		default:
			b.refused++
		}
		b.mu.Unlock()
		// A page refused is aborted, so the tab keeps the page it shows
		// rather than an error page: a form a page sends on its own leaves
		// the page where it was.
		reason := "BlockedByClient"
		if kind == "Document" {
			reason = "Aborted"
		}
		b.c.call(ctx, session, "Fetch.failRequest", map[string]any{"requestId": id, "errorReason": reason}, nil)
		return
	}
	if u, err := url.Parse(address); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		b.mu.Lock()
		b.asked[strings.ToLower(u.Hostname())] = true
		b.mu.Unlock()
	}
	b.c.call(ctx, session, "Fetch.continueRequest", map[string]any{"requestId": id}, nil)
}

func (b *chromium) note(s string) {
	b.mu.Lock()
	b.notes = append(b.notes, s)
	b.mu.Unlock()
}

func (b *chromium) Notes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.notes
	if b.refused > 0 {
		out = append(out, fmt.Sprintf("Keystone refused %d other requests of the page: to blocked sites, or to addresses that are not on the public internet.", b.refused))
	}
	b.notes, b.refused = nil, 0
	return dedupe(out)
}

func (b *chromium) Asked() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.asked))
	for h := range b.asked {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func (b *chromium) SetSending(on bool) { b.sending.Store(on) }

func (b *chromium) Done() <-chan struct{} { return b.done }

// eval runs an expression in the tab and puts its value, which must be a
// JSON string, in out.
func (b *chromium) eval(ctx context.Context, expr string, out any) error {
	var r struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := b.c.call(ctx, b.session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}, &r); err != nil {
		return err
	}
	if r.ExceptionDetails != nil {
		return errors.New("the page could not be read")
	}
	if out == nil {
		return nil
	}
	var s string
	if json.Unmarshal(r.Result.Value, &s) != nil {
		return errors.New("the page could not be read")
	}
	return json.Unmarshal([]byte(s), out)
}

// settle waits for the tab to finish loading, until ctx ends. A page still
// loading then is read as it is.
func (b *chromium) settle(ctx context.Context, first time.Duration) {
	select {
	case <-time.After(first):
	case <-ctx.Done():
		return
	}
	for {
		var state string
		if b.eval(ctx, `JSON.stringify(document.readyState)`, &state) == nil && state == "complete" {
			return
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
}

func (b *chromium) Navigate(ctx context.Context, address string) error {
	b.mu.Lock()
	b.lastWhy = ""
	b.mu.Unlock()
	var r struct {
		ErrorText  string `json:"errorText"`
		IsDownload bool   `json:"isDownload"`
	}
	if err := b.c.call(ctx, b.session, "Page.navigate", map[string]any{"url": address}, &r); err != nil {
		return err
	}
	switch {
	case r.IsDownload:
		return errors.New("the address is a download, and the browser never downloads")
	case r.ErrorText != "":
		b.mu.Lock()
		why := b.lastWhy
		b.mu.Unlock()
		if why != "" {
			return errors.New(why)
		}
		return fmt.Errorf("the page could not be opened (%s)", r.ErrorText)
	}
	b.settle(ctx, 50*time.Millisecond)
	return nil
}

// describeJS is the page's side of reading an element, shared by the
// functions below. It never changes the page but for the data-keystone-ref
// marks it reads.
const describeJS = `
function ksDescribe(el, n) {
  const tag = el.tagName.toLowerCase();
  const type = (el.getAttribute('type') || (tag === 'button' ? 'submit' : '')).toLowerCase();
  const form = el.form || el.closest('form');
  const inPw = !!(form && form.querySelector('input[type=password]'));
  const role = el.getAttribute('role') || '';
  let label = el.getAttribute('aria-label') || '';
  if (!label && el.labels && el.labels.length) label = el.labels[0].innerText;
  if (!label && (tag === 'a' || tag === 'button' || tag === 'summary' || role)) label = el.innerText;
  if (!label && tag === 'input' && (type === 'submit' || type === 'button' || type === 'reset')) label = el.value;
  if (!label) label = el.getAttribute('title') || el.getAttribute('alt') || '';
  const noText = ['submit','button','reset','checkbox','radio','file','image','hidden','range','color'];
  const editable = (tag === 'textarea' || (tag === 'input' && !noText.includes(type)) || el.isContentEditable || role === 'textbox') && !el.disabled && !el.readOnly;
  let action = '';
  if (form) { try { action = el.formAction || form.action || ''; } catch (e) { action = form.action || ''; } }
  return {ref: n, tag: tag, type: type, role: role, name: el.getAttribute('name') || '', id: el.id || '',
    autocomplete: el.getAttribute('autocomplete') || '', inputmode: el.getAttribute('inputmode') || '',
    label: String(label || '').replace(/\s+/g, ' ').trim().slice(0, 120), placeholder: el.getAttribute('placeholder') || '',
    editable: !!editable, inPasswordForm: inPw, href: tag === 'a' ? (el.href || '') : '', formAction: String(action)};
}
`

type described struct {
	Ref            int    `json:"ref"`
	Tag            string `json:"tag"`
	Type           string `json:"type"`
	Role           string `json:"role"`
	Name           string `json:"name"`
	ID             string `json:"id"`
	Autocomplete   string `json:"autocomplete"`
	InputMode      string `json:"inputmode"`
	Label          string `json:"label"`
	Placeholder    string `json:"placeholder"`
	Editable       bool   `json:"editable"`
	InPasswordForm bool   `json:"inPasswordForm"`
	Href           string `json:"href"`
	FormAction     string `json:"formAction"`
}

func (d described) element() guard.Element {
	return guard.Element{Tag: d.Tag, Type: d.Type, Role: d.Role, Name: d.Name, ID: d.ID, Autocomplete: d.Autocomplete, InputMode: d.InputMode,
		Label: d.Label, Placeholder: d.Placeholder, Editable: d.Editable, InPasswordForm: d.InPasswordForm, Href: d.Href, FormAction: d.FormAction}
}

func (b *chromium) Page(ctx context.Context, maxElements int) (Page, error) {
	expr := describeJS + fmt.Sprintf(`
(() => {
  const max = %d;
  if (max < 0) return JSON.stringify({url: location.href, title: document.title || '', text: '', elements: []});
  const sel = 'a[href],button,input:not([type=hidden]),textarea,select,summary,[role=button],[role=link],[role=checkbox],[role=textbox],[contenteditable=""],[contenteditable=true]';
  document.querySelectorAll('[data-keystone-ref]').forEach(e => e.removeAttribute('data-keystone-ref'));
  const out = [];
  let n = 0;
  for (const el of document.querySelectorAll(sel)) {
    if (n >= max) break;
    const r = el.getBoundingClientRect();
    const st = getComputedStyle(el);
    if ((r.width === 0 && r.height === 0) || st.visibility === 'hidden' || st.display === 'none') continue;
    n++;
    el.setAttribute('data-keystone-ref', String(n));
    out.push(ksDescribe(el, n));
  }
  return JSON.stringify({url: location.href, title: document.title || '', text: document.body ? document.body.innerText : '', elements: out});
})()`, maxElements)
	var got struct {
		URL      string      `json:"url"`
		Title    string      `json:"title"`
		Text     string      `json:"text"`
		Elements []described `json:"elements"`
	}
	if err := b.eval(ctx, expr, &got); err != nil {
		return Page{}, err
	}
	p := Page{URL: got.URL, Title: got.Title, Text: got.Text}
	for _, d := range got.Elements {
		p.Elements = append(p.Elements, Element{Ref: d.Ref, Kind: guard.Kind(d.element()), Label: d.Label, Href: d.Href})
	}
	return p, nil
}

func (b *chromium) Element(ctx context.Context, ref int) (guard.Element, error) {
	var d *described
	expr := describeJS + fmt.Sprintf(`(() => { const el = document.querySelector('[data-keystone-ref="%d"]'); return JSON.stringify(el ? ksDescribe(el, %d) : null); })()`, ref, ref)
	if err := b.eval(ctx, expr, &d); err != nil {
		return guard.Element{}, err
	}
	if d == nil {
		return guard.Element{}, ErrNoElement
	}
	return d.element(), nil
}

func (b *chromium) Click(ctx context.Context, ref int) error {
	var at *struct {
		X, Y float64
		Hit  bool
	}
	expr := fmt.Sprintf(`(() => {
  const el = document.querySelector('[data-keystone-ref="%d"]');
  if (!el) return JSON.stringify(null);
  if (el.tagName.toLowerCase() === 'a') el.target = '_self';
  if (el.form) el.form.target = '_self';
  el.scrollIntoView({block: 'center', inline: 'center'});
  const r = el.getBoundingClientRect();
  const x = r.left + r.width / 2, y = r.top + r.height / 2;
  const top = document.elementFromPoint(x, y);
  return JSON.stringify({X: x, Y: y, Hit: !!top && (top === el || el.contains(top))});
})()`, ref)
	if err := b.eval(ctx, expr, &at); err != nil {
		return err
	}
	if at == nil {
		return ErrNoElement
	}
	if at.Hit {
		for _, kind := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
			p := map[string]any{"type": kind, "x": at.X, "y": at.Y}
			if kind != "mouseMoved" {
				p["button"], p["clickCount"] = "left", 1
			}
			if err := b.c.call(ctx, b.session, "Input.dispatchMouseEvent", p, nil); err != nil {
				return err
			}
		}
	} else if err := b.eval(ctx, fmt.Sprintf(`(() => { const el = document.querySelector('[data-keystone-ref="%d"]'); if (el) el.click(); return JSON.stringify(true); })()`, ref), nil); err != nil {
		// Something lay over the element, such as a banner, so it was
		// clicked as a script would click it, and that failed.
		return err
	}
	b.settle(ctx, 400*time.Millisecond)
	return nil
}

func (b *chromium) Type(ctx context.Context, ref int, text string, check func(guard.Element) error) error {
	var d *described
	expr := describeJS + fmt.Sprintf(`(() => {
  const el = document.querySelector('[data-keystone-ref="%d"]');
  if (!el) return JSON.stringify(null);
  el.scrollIntoView({block: 'center'});
  el.focus();
  const a = document.activeElement;
  if (!a || a.getAttribute('data-keystone-ref') !== '%d') return JSON.stringify(null);
  return JSON.stringify(ksDescribe(a, %d));
})()`, ref, ref, ref)
	if err := b.eval(ctx, expr, &d); err != nil {
		return err
	}
	if d == nil {
		return ErrNoElement
	}
	// The element is checked again as it has the focus, so a page that
	// changes a field as it is focused is caught.
	if err := check(d.element()); err != nil {
		return err
	}
	if err := b.c.call(ctx, b.session, "Input.insertText", map[string]any{"text": text}, nil); err != nil {
		return err
	}
	b.settle(ctx, 200*time.Millisecond)
	return nil
}

func (b *chromium) Back(ctx context.Context) error {
	var h struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int `json:"id"`
		} `json:"entries"`
	}
	if err := b.c.call(ctx, b.session, "Page.getNavigationHistory", nil, &h); err != nil {
		return err
	}
	if h.CurrentIndex < 1 || h.CurrentIndex >= len(h.Entries) {
		return errors.New("there is no page to go back to")
	}
	if err := b.c.call(ctx, b.session, "Page.navigateToHistoryEntry", map[string]any{"entryId": h.Entries[h.CurrentIndex-1].ID}, nil); err != nil {
		return err
	}
	b.settle(ctx, 200*time.Millisecond)
	return nil
}

func (b *chromium) Screenshot(ctx context.Context) ([]byte, error) {
	var r struct {
		Data string `json:"data"`
	}
	if err := b.c.call(ctx, b.session, "Page.captureScreenshot", map[string]any{"format": "jpeg", "quality": 70}, &r); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(r.Data)
}

func (b *chromium) Cookies(ctx context.Context) ([]guard.Cookie, error) {
	var r struct {
		Cookies []struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		} `json:"cookies"`
	}
	if err := b.c.call(ctx, "", "Storage.getCookies", nil, &r); err != nil {
		return nil, err
	}
	out := make([]guard.Cookie, 0, len(r.Cookies))
	for _, c := range r.Cookies {
		out = append(out, guard.Cookie{Name: c.Name, Domain: c.Domain})
	}
	return out, nil
}

// Close asks the browser to close, and ends it if it does not within a
// few seconds.
func (b *chromium) Close() error {
	if !b.closed.CompareAndSwap(false, true) {
		<-b.done
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b.c.call(ctx, "", "Browser.close", nil, nil)
	select {
	case <-b.p.wait:
	case <-time.After(5 * time.Second):
		b.p.kill()
		<-b.p.wait
	}
	b.p.release()
	<-b.done
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func clip(s string, most int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= most {
		return s
	}
	return string(r[:most-1]) + "…"
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
