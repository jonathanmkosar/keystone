package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/jonathanmkosar/keystone/settings"
	"github.com/jonathanmkosar/keystone/web"
)

// handler serves the web interface that is compiled into the binary.
// The API shell (P2-05) takes over this mux and adds the API routes.
func handler() http.Handler {
	files := http.FileServerFS(web.Files())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func runServe(args []string, out, errw io.Writer) int {
	reg, err := settings.Registry()
	if err != nil {
		fmt.Fprintln(errw, "keystone: the settings registry is broken:", err)
		return 1
	}
	def, _ := settings.Lookup(reg, "server.address")
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(errw)
	address := fl.String("address", def.Default, "the address and port to listen on")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := serve(ctx, *address, out); err != nil {
		fmt.Fprintln(errw, "keystone:", err)
		return 1
	}
	return 0
}

// serve listens on address and serves until ctx is done. It prints the
// address it is really listening on, so a caller that asked for port 0
// learns the port.
func serve(ctx context.Context, address string, out io.Writer) error {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler(), ReadHeaderTimeout: 10 * time.Second}
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
