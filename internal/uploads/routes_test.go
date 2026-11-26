package uploads

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Invented, for throwaway folders only.
const (
	phraseOne = "uploads person one's phrase"
	phraseTwo = "uploads person two's phrase"
)

// apiBodyLimit is the API's own limit on a body in these tests, smaller
// than the largest upload, so the upload route's larger limit is proven.
const apiBodyLimit = 1024

type routeRig struct {
	t        *testing.T
	h        http.Handler
	in       *tenant.Install
	one, two string
	tokens   map[string]string
}

func newRouteRig(t *testing.T) *routeRig {
	t.Helper()
	in, a, b := stores(t)
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	g := &routeRig{t: t, in: in, tokens: map[string]string{}, one: a.TenantID(), two: b.TenantID()}
	for s, phrase := range map[*tenant.Store]string{a: phraseOne, b: phraseTwo} {
		if err := signin.SetPassword(s, phrase, o); err != nil {
			t.Fatal(err)
		}
		token, _, err := signin.SignIn(s, phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		g.tokens[s.TenantID()] = token
	}
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: apiBodyLimit, Heartbeat: time.Minute}, Routes(testOptions())); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *routeRig) chat(who string) string {
	g.t.Helper()
	s, err := g.in.Store(who)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	c, err := chat.Create(s)
	if err != nil {
		g.t.Fatal(err)
	}
	return c.ID
}

func (g *routeRig) call(who, method, path string, body []byte) *httptest.ResponseRecorder {
	g.t.Helper()
	r := httptest.NewRequest(method, api.Prefix+path, bytes.NewReader(body))
	r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[who]})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

func (g *routeRig) upload(who, chatID, name string, data []byte) *httptest.ResponseRecorder {
	return g.call(who, "POST", "chats/"+chatID+"/uploads?name="+url.QueryEscape(name), data)
}

// noisyPNG is an invented picture whose pixels change all over, so it does
// not compress to a few bytes.
func noisyPNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for x := 0; x < side; x++ {
		for y := 0; y < side; y++ {
			img.Set(x, y, color.RGBA{uint8(x*37 + y*91), uint8(x*x + y*13), uint8(x ^ y*7), 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("the answer does not read: %v\n%s", err, rec.Body)
	}
	return v
}

func code(rec *httptest.ResponseRecorder) string {
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Code
}

func TestTheRoutes(t *testing.T) {
	g := newRouteRig(t)
	chatID := g.chat(g.one)
	picture := noisyPNG(t, 48)
	if len(picture) <= apiBodyLimit {
		t.Fatalf("the test picture is %d bytes and must be larger than the API's own limit", len(picture))
	}

	// An upload larger than the API's own limit on a body is taken.
	rec := g.upload(g.one, chatID, "square.png", picture)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the upload answered %d: %s", rec.Code, rec.Body)
	}
	up := decode[AddReply](t, rec)
	if !up.Added || up.ID != hashOf(picture) || up.Kind != KindImage || up.Name != "square.png" {
		t.Errorf("the upload answered %+v", up)
	}
	if rec := g.upload(g.one, chatID, "square.png", picture); rec.Code != http.StatusOK || decode[AddReply](t, rec).Added {
		t.Errorf("the same file again answered %d: %s", rec.Code, rec.Body)
	}
	// A body larger than the most one upload may be is refused.
	if rec := g.upload(g.one, chatID, "big.txt", bytes.Repeat([]byte("x"), int(testOptions().MaxBytes)+1)); rec.Code != http.StatusRequestEntityTooLarge || code(rec) != "too_large" {
		t.Errorf("a file over the limit answered %d %s", rec.Code, rec.Body)
	}
	if rec := g.upload(g.one, chatID, "program.exe", []byte{'M', 'Z', 0x90, 0}); rec.Code != http.StatusBadRequest || code(rec) != "bad_upload" {
		t.Errorf("a file that is not taken answered %d %s", rec.Code, rec.Body)
	}
	page := []byte("<html><body><h1>Invented</h1><script>alert(1)</script><p>words</p></body></html>")
	rec = g.upload(g.one, chatID, "page.html", page)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the page answered %d: %s", rec.Code, rec.Body)
	}
	pageUp := decode[AddReply](t, rec)

	// The list, one, and the text read.
	list := decode[ListReply](t, g.call(g.one, "GET", "chats/"+chatID+"/uploads", nil))
	if len(list.Uploads) != 2 || list.Notices == nil {
		t.Errorf("the list is %+v", list)
	}
	one := decode[OneReply](t, g.call(g.one, "GET", "chats/"+chatID+"/uploads/"+pageUp.ID, nil))
	if one.Text != "Invented\n\nwords" || one.Kind != KindText || one.MediaType != "text/html" {
		t.Errorf("one upload reads %+v", one)
	}

	// The bytes: an image as itself, a page as a download no browser runs.
	rec = g.call(g.one, "GET", "chats/"+chatID+"/uploads/"+up.ID+"/content", nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), picture) || rec.Header().Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("the image's bytes answered %d %v", rec.Code, rec.Header())
	}
	rec = g.call(g.one, "GET", "chats/"+chatID+"/uploads/"+pageUp.ID+"/content", nil)
	if rec.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") || !bytes.Equal(rec.Body.Bytes(), page) {
		t.Errorf("the page's bytes were served with %v", rec.Header())
	}

	// Tenant two sees none of it, whatever it names.
	for _, path := range []string{
		"chats/" + chatID + "/uploads",
		"chats/" + chatID + "/uploads/" + up.ID,
		"chats/" + chatID + "/uploads/" + up.ID + "/content",
	} {
		if rec := g.call(g.two, "GET", path, nil); rec.Code != http.StatusNotFound || bytes.Contains(rec.Body.Bytes(), picture[:16]) {
			t.Errorf("tenant two: GET %s answered %d", path, rec.Code)
		}
	}
	if rec := g.call(g.two, "DELETE", "chats/"+chatID+"/uploads/"+up.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("tenant two removed tenant one's upload: %d", rec.Code)
	}
	if rec := g.upload(g.two, chatID, "theirs.txt", []byte("tenant two's words")); rec.Code != http.StatusNotFound {
		t.Errorf("tenant two uploaded to tenant one's chat: %d", rec.Code)
	}
	theirs := g.chat(g.two)
	if rec := g.call(g.two, "GET", "chats/"+theirs+"/uploads/"+up.ID+"/content", nil); rec.Code != http.StatusNotFound {
		t.Errorf("tenant two read tenant one's file through a chat of its own: %d", rec.Code)
	}
	for _, id := range []string{"..", "..%2F..%2Fkeystone.db", "not-a-hash", strings.ToUpper(up.ID)} {
		if rec := g.call(g.one, "GET", "chats/"+chatID+"/uploads/"+id+"/content", nil); rec.Code == http.StatusOK {
			t.Errorf("the id %q answered %d", id, rec.Code)
		}
	}

	// Remove, and the limits.
	if rec := g.call(g.one, "DELETE", "chats/"+chatID+"/uploads/"+pageUp.ID, nil); rec.Code != http.StatusOK {
		t.Errorf("removing answered %d %s", rec.Code, rec.Body)
	}
	if rec := g.call(g.one, "GET", "chats/"+chatID+"/uploads/"+pageUp.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("the removed upload answered %d", rec.Code)
	}
	limits := decode[Options](t, g.call(g.one, "GET", "uploads/limits", nil))
	if limits.MaxBytes != testOptions().MaxBytes || len(limits.ImageTypes) == 0 {
		t.Errorf("the limits read %+v", limits)
	}
}

func TestReadOptionsReadsTheShippedSettings(t *testing.T) {
	values := map[string]string{
		SettingMaxBytes: "5242880", SettingImageTypes: "image/png, IMAGE/JPEG,", SettingTextExtensions: ".txt, md", SettingDocumentTypes: "application/pdf",
	}
	o, err := ReadOptions(func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if o.MaxBytes != 5242880 || len(o.ImageTypes) != 2 || o.ImageTypes[1] != "image/jpeg" || o.TextExtensions[1] != ".md" {
		t.Errorf("read %+v", o)
	}
	values[SettingImageTypes] = "text/html"
	if _, err := ReadOptions(func(k string) (string, bool) { v, ok := values[k]; return v, ok }); err == nil {
		t.Error("an image type that is not an image was taken")
	}
}
