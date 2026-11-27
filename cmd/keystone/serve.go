package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
	"github.com/jonathanmkosar/keystone/web"
)

// handler serves the API under /api/ and, everywhere else, the web
// interface that is compiled into the binary. With no API it serves the
// page alone.
func handler(api http.Handler) http.Handler {
	files := http.FileServerFS(web.Files())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("Permissions-Policy", "microphone=(self)") // dictation, from Keystone's own pages only (decision 0500)
		h.Add("Permissions-Policy", "geolocation=(self)") // location per message, on a tap, from Keystone's own pages only (decision 0602)
		if api != nil && (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// The interface routes in the browser, so a path that is not a file,
		// such as /chats, is the page itself (decision 0071). A path under
		// /api never reaches here when there is an API, and is never the page.
		if isRoute(r.URL.Path) {
			page, err := fs.ReadFile(web.Files(), "index.html")
			if err != nil {
				http.Error(w, "the page is missing from this build", http.StatusInternalServerError)
				return
			}
			h.Set("Content-Type", "text/html; charset=utf-8")
			h.Set("Cache-Control", "no-cache")
			if r.Method == http.MethodGet {
				w.Write(page)
			}
			return
		}
		// A file under /assets has its content's hash in its name, so a browser
		// may keep it for good. Every other file keeps its name from build to
		// build (keystone.css, theme.js, mark.svg), so a browser must ask again
		// each time, and an ETag of its content lets it keep its copy when
		// nothing changed. Without this a phone kept an old stylesheet after a
		// release (November 27, 2026).
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
			if tag, ok := etagOf(r.URL.Path); ok {
				h.Set("ETag", tag)
			}
		}
		files.ServeHTTP(w, r)
	})
}

// etags holds the ETag of each built file, worked out once from its bytes.
var etags sync.Map

// etagOf returns the ETag of the built file a path names, or false when
// there is no such file.
func etagOf(p string) (string, bool) {
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name == "" {
		name = "index.html"
	}
	if tag, ok := etags.Load(name); ok {
		return tag.(string), true
	}
	b, err := fs.ReadFile(web.Files(), name)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	tag := `"` + hex.EncodeToString(sum[:12]) + `"`
	etags.Store(name, tag)
	return tag, true
}

// isRoute reports whether a request path is a route of the interface and
// not a file: it is not the root, has no file extension, is not under /api,
// and names no file in the built interface. A missing file stays a 404.
func isRoute(p string) bool {
	name := strings.TrimPrefix(path.Clean("/"+p), "/")
	if name == "" || path.Ext(name) != "" || name == "api" || strings.HasPrefix(name, "api/") {
		return false
	}
	_, err := fs.Stat(web.Files(), name)
	return err != nil
}

// setting returns the shipped value of a setting.
func setting(reg []settings.Setting, key string) (string, error) {
	s, ok := settings.Lookup(reg, key)
	if !ok {
		return "", fmt.Errorf("the %s setting is missing from the registry", key)
	}
	return s.Default, nil
}

func runServe(args []string, out, errw io.Writer) int {
	reg, err := settings.Registry()
	if err != nil {
		fmt.Fprintln(errw, "keystone: the settings registry is broken:", err)
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(errw, "keystone:", err)
		return 1
	}
	defAddress, err := setting(reg, "server.address")
	if err != nil {
		return fail(err)
	}
	defData, err := setting(reg, "data.dir")
	if err != nil {
		return fail(err)
	}
	keeper, err := setting(reg, "secrets.keeper")
	if err != nil {
		return fail(err)
	}
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(errw)
	address := fl.String("address", defAddress, "the address and port to listen on")
	data := fl.String("data", defData, "the data folder; a relative folder is read from where keystone is started")
	if err := fl.Parse(args); err != nil {
		return 2
	}

	api, err := openAPI(*data, keeper, errw)
	if err != nil {
		return fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := serve(ctx, *address, handler(api), out); err != nil {
		return fail(err)
	}
	return 0
}

// openAPI opens the data folder and returns the API's handler for it, with
// the shipped settings. keeper is the secrets.keeper setting: the tenant
// package reads no setting, so the server tells it where an install with
// no master key yet keeps one. The server's own log goes to log.
func openAPI(dataDir, keeper string, log io.Writer) (http.Handler, error) {
	in, err := tenant.Open(dataDir)
	if err != nil {
		return nil, err
	}
	in.NewInstallKeeper(keeper)
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	set, err := adapters.Real(reg)
	if err != nil {
		return nil, err
	}
	c.Install, c.Log, c.Adapters = in, log, set
	return app.Handler(c)
}

// serve listens on address and serves h until ctx is done. It prints the
// address it is really listening on, so a caller that asked for port 0
// learns the port.
func serve(ctx context.Context, address string, h http.Handler, out io.Writer) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	// No write timeout: a stream of server-sent events stays open.
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(out, "Keystone is listening on http://%s\n", ln.Addr())

	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()
	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(wait); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
