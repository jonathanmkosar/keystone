package settingsapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A model's ca_cert is PEM text or the path of a PEM file, and the
// Chat Completions adapter reads a path with the server's own rights when
// the model is opened. Through the API that would let any person make
// Keystone read any path the server can reach: another person's folder, a
// device, or a share on another host that the system signs in to. The
// isolation drill (P13-02) found it; decision 0490 closes it here, in the
// routes, so the registry and the command line are unchanged.
//
// The API takes ca_cert as PEM text. A path is set from the command line
// (go run ./tools/tenant model add), as a connector that runs a program is
// (decision 0310), and an entry that already holds a path keeps it when it
// is changed through the API with the same value.

// caCertIsPath reports whether a ca_cert value is a path and not PEM text,
// told apart as the registry's check and the adapter tell them apart.
func caCertIsPath(value string) bool {
	return value != "" && !strings.Contains(value, "-----BEGIN")
}

// refuseCACertPath answers and returns true when an entry sent through the
// API names a ca_cert path that is not the one the entry already holds.
// held is the stored entry's ca_cert, or empty for a new entry. The path is
// not said back.
func refuseCACertPath(w http.ResponseWriter, r *http.Request, m models.Model, held string) bool {
	if !caCertIsPath(m.CACert) || m.CACert == held {
		return false
	}
	api.Error(w, r, http.StatusBadRequest, "bad_model",
		"ca_cert is the certificate authority's certificate as PEM text here: the path of a file on this machine is set from the command line, with go run ./tools/tenant model add")
	return true
}

// heldCACert returns the ca_cert of the stored entry with the name, or
// empty when there is none.
func heldCACert(ctx context.Context, s *tenant.Store, name string) string {
	entries, err := models.List(ctx, s)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Model.Name == name {
			return e.Model.CACert
		}
	}
	return ""
}
