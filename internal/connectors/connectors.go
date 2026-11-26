// Package connectors is each person's connectors: MCP servers whose tools a
// chat may use (P7-04). A connector is a local command spoken to over stdio,
// or a remote server reached over the streamable HTTP transport. The
// protocol is internal/mcp's; this package keeps the connectors, decides
// what a turn is offered, opens and keeps sessions, and serves the routes.
//
// Three rules hold here:
//
//   - A connector's address and command come from the person's own record
//     and nothing else. A model, a tool's result or a server can never name
//     one. A command runs only as configured, with no shell, and is added
//     from the command line only, never through the API, so a person signed
//     in through the browser cannot run a program on the machine.
//   - A remote server's address must be a public HTTPS address, checked as
//     it connects, or one the person marked as their own local server,
//     which must then be on this machine or a private network. No proxy is
//     used and no redirect is followed.
//   - A token is read from the person's vault for each session, is sent to
//     the server and nowhere else, and is taken out of anything the server
//     sends back before Keystone keeps or shows it.
//
// docs/architecture/connectors.md is the whole of it.
package connectors

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/mcp"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// The transports a connector may have.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	// TransportGoogle is a built-in connector: Gmail or Google Calendar,
	// whose tools are Keystone's own, reached with the person's Google
	// sign-in (P12-02, google.go).
	TransportGoogle = "google"
)

// Who added a connector.
const (
	ByPerson      = "person"
	ByCommandLine = "command line"
)

// Connector is one connector as a person or the command line gives it.
type Connector struct {
	// Name starts the name of each of its tools: lower-case letters, digits
	// and underscores, starting with a letter, at most 24.
	Name string `json:"name"`
	// Label is what the screens call it. Empty means its name.
	Label string `json:"label"`
	// Transport is stdio or http.
	Transport string `json:"transport"`

	// Command, Args and Env are a stdio connector's: the program, its
	// arguments, and plain environment variables it is given. A token is
	// never written in them: it is stored in the vault and passed in
	// TokenEnv.
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	TokenEnv string            `json:"token_env,omitempty"`

	// URL is an http connector's MCP endpoint. Local says the person marked
	// it as their own server on this machine or their home network: it may
	// then be plain http, and must not be a public address.
	URL   string `json:"url,omitempty"`
	Local bool   `json:"local,omitempty"`

	// Secret names the vault entry that holds its token, such as
	// connector.notes. Empty means it takes none. An http connector sends
	// the token as a bearer token.
	Secret string `json:"secret,omitempty"`
	// Enabled says its tools may be offered. Off, it offers nothing in any
	// chat.
	Enabled bool `json:"enabled"`
	// Outside says its results are outside content: once a chat has read
	// one, every tool that changes something asks. True unless the person
	// says otherwise.
	Outside bool `json:"outside"`
}

// Tool is one tool a connector listed, as it is kept.
type Tool struct {
	// Name is the name a chat offers it by: the connector's name and the
	// server's name for it.
	Name string `json:"name"`
	// Tool is the server's own name for it.
	Tool        string          `json:"tool"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	// ReadOnly says the server says it only reads.
	ReadOnly bool `json:"read_only"`
}

// Kept is a connector as the person's record holds it.
type Kept struct {
	Connector
	AddedBy string    `json:"added_by"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Listed is when its tools were last listed. Zero means never.
	Listed time.Time `json:"listed,omitzero"`
	// Problem is why the last try to reach it failed. Empty when it did
	// not.
	Problem string `json:"problem,omitempty"`
	// Tools are the tools it last listed, each with the name a chat offers
	// it by.
	Tools []Tool `json:"tools"`
}

// DisplayName is the label, or the name when it has none.
func (c Connector) DisplayName() string {
	if strings.TrimSpace(c.Label) != "" {
		return c.Label
	}
	return c.Name
}

// Errors the routes turn into answers.
var (
	ErrNotFound = errors.New("there is no connector of that name")
	ErrExists   = errors.New("a connector of that name is already there")
	ErrFull     = errors.New("the most connectors one person may have are there")
	// ErrBad wraps the reason a connector is refused.
	ErrBad = errors.New("the connector is refused")
)

var (
	nameShape    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)
	secretShape  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)
	envNameShape = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBad, fmt.Sprintf(format, args...))
}

// plainText reports whether a value is one line of printable text.
func plainText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return r == 0 || r == '\n' || r == '\r' || !unicode.IsPrint(r) && r != ' ' && r != '\t'
	})
}

// Check says what is wrong with a connector, in words that name the field
// and never repeat a value, since a token may have been pasted in the wrong
// place.
func Check(c Connector) error {
	switch {
	case !nameShape.MatchString(c.Name) || strings.HasSuffix(c.Name, "_") || strings.Contains(c.Name, "__"):
		return bad("its name must be lower-case letters, digits and single underscores, starting with a letter, at most 24")
	case utf8.RuneCountInString(c.Label) > 60 || !plainText(c.Label):
		return bad("its label must be one line of at most 60 characters")
	case c.Secret != "" && !secretShape.MatchString(c.Secret):
		return bad("its token's name is not the name of a secret: lower-case letters, digits, dots, dashes and underscores")
	}
	switch c.Transport {
	case TransportStdio:
		return checkStdio(c)
	case TransportHTTP:
		return checkHTTP(c)
	case TransportGoogle:
		return checkGoogle(c) // P12-02, google.go
	}
	return bad("its transport must be stdio or http")
}

func checkStdio(c Connector) error {
	switch {
	case strings.TrimSpace(c.Command) == "" || len(c.Command) > 1000 || !plainText(c.Command):
		return bad("a stdio connector needs a command: one line of at most 1000 characters")
	case len(c.Args) > 64:
		return bad("a stdio connector may have at most 64 arguments")
	case len(c.Env) > 32:
		return bad("a stdio connector may have at most 32 environment variables")
	case c.URL != "" || c.Local:
		return bad("a stdio connector has no address")
	case c.TokenEnv != "" && !envNameShape.MatchString(c.TokenEnv):
		return bad("the variable its token is passed in must be a variable's name")
	case c.TokenEnv != "" && c.Secret == "":
		return bad("it names a variable for its token and no token")
	}
	for _, a := range c.Args {
		if len(a) > 2000 || !utf8.ValidString(a) || strings.ContainsRune(a, 0) {
			return bad("each argument must be text of at most 2000 bytes")
		}
	}
	for k, v := range c.Env {
		if !envNameShape.MatchString(k) || len(v) > 2000 || !plainText(v) {
			return bad("each environment variable must have a variable's name and one line of at most 2000 bytes")
		}
		if strings.EqualFold(k, c.TokenEnv) {
			return bad("an environment variable has the name its token is passed in")
		}
	}
	return nil
}

func checkHTTP(c Connector) error {
	switch {
	case c.Command != "" || len(c.Args) > 0 || len(c.Env) > 0 || c.TokenEnv != "":
		return bad("an http connector has no command")
	case len(c.URL) > 2000:
		return bad("its address is longer than 2000 characters")
	}
	u, err := url.Parse(c.URL)
	switch {
	case err != nil || u.Host == "" || u.Hostname() == "":
		return bad("its address must be a whole address, such as https://mcp.example/mcp")
	case u.User != nil:
		return bad("its address may not hold a user name or a password: store a token instead")
	case u.Fragment != "":
		return bad("its address may not end with a #part")
	case u.Scheme != "https" && !(c.Local && u.Scheme == "http"):
		if c.Local {
			return bad("its address must start with http:// or https://")
		}
		return bad("its address must start with https://, unless it is your own server on this machine or your home network")
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil {
		switch {
		case c.Local && web.IsPublic(a):
			return bad("it is marked as your own local server, and its address is on the public internet")
		case !c.Local && !web.IsPublic(a):
			return bad("its address is on this machine or a private network: mark it as your own local server if it is")
		}
		return nil
	}
	if !c.Local && web.IsLocalName(host) {
		return bad("its address names this machine or a local network: mark it as your own local server if it is")
	}
	return nil
}

// normal fills what a connector leaves out.
func normal(c Connector) Connector {
	c.Label = strings.TrimSpace(c.Label)
	c.Command = strings.TrimSpace(c.Command)
	c.URL = strings.TrimSpace(c.URL)
	if c.Args == nil {
		c.Args = []string{}
	}
	if c.Env == nil {
		c.Env = map[string]string{}
	}
	return c
}

const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

const columns = `name, label, transport, command, args, env, url, local, secret, token_env, enabled, outside, added_by, listed_at, problem, created_at, updated_at`

func scan(sc interface{ Scan(...any) error }) (Kept, error) {
	var k Kept
	var args, env, listed, created, updated string
	err := sc.Scan(&k.Name, &k.Label, &k.Transport, &k.Command, &args, &env, &k.URL, &k.Local, &k.Secret, &k.TokenEnv,
		&k.Enabled, &k.Outside, &k.AddedBy, &listed, &k.Problem, &created, &updated)
	if err != nil {
		return Kept{}, err
	}
	if json.Unmarshal([]byte(args), &k.Args) != nil || json.Unmarshal([]byte(env), &k.Env) != nil {
		return Kept{}, fmt.Errorf("connector %s: its stored command does not read", k.Name)
	}
	for _, x := range []struct {
		text string
		into *time.Time
	}{{created, &k.Created}, {updated, &k.Updated}, {listed, &k.Listed}} {
		if x.text == "" {
			continue
		}
		if *x.into, err = time.Parse(timeLayout, x.text); err != nil {
			return Kept{}, fmt.Errorf("connector %s: %w", k.Name, err)
		}
	}
	return k, nil
}

// List returns the person's connectors, by name, each with the tools it
// last listed.
func List(ctx context.Context, s *tenant.Store) ([]Kept, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+columns+" FROM connectors ORDER BY name")
	if err != nil {
		return nil, err
	}
	var out []Kept
	for rows.Next() {
		k, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Tools, err = keptTools(ctx, s, out[i].Name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get returns one connector.
func Get(ctx context.Context, s *tenant.Store, name string) (Kept, error) {
	k, err := scan(s.DB().QueryRowContext(ctx, "SELECT "+columns+" FROM connectors WHERE name = ?", name))
	if errors.Is(err, sql.ErrNoRows) {
		return Kept{}, ErrNotFound
	}
	if err != nil {
		return Kept{}, err
	}
	k.Tools, err = keptTools(ctx, s, name)
	return k, err
}

func keptTools(ctx context.Context, s *tenant.Store, connector string) ([]Tool, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT tool, title, description, schema, read_only FROM connector_tools WHERE connector = ? ORDER BY position", connector)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tool{}
	var theirs []string
	for rows.Next() {
		var t Tool
		var schema string
		if err := rows.Scan(&t.Tool, &t.Title, &t.Description, &schema, &t.ReadOnly); err != nil {
			return nil, err
		}
		t.Schema = json.RawMessage(schema)
		out = append(out, t)
		theirs = append(theirs, t.Tool)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := ToolNames(connector, theirs)
	for i := range out {
		out[i].Name = names[i]
	}
	return out, nil
}

// Add keeps a new connector. by says who added it: ByPerson or
// ByCommandLine. max is the most one person may have.
func Add(ctx context.Context, s *tenant.Store, c Connector, by string, max int) (Kept, error) {
	c = normal(c)
	if err := Check(c); err != nil {
		return Kept{}, err
	}
	args, _ := json.Marshal(c.Args)
	env, _ := json.Marshal(c.Env)
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM connectors").Scan(&n); err != nil {
			return err
		}
		var taken int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM connectors WHERE name = ?", c.Name).Scan(&taken); err != nil {
			return err
		}
		switch {
		case taken > 0:
			return ErrExists
		case max > 0 && n >= max:
			return ErrFull
		}
		now := stamp()
		_, err := tx.ExecContext(ctx, "INSERT INTO connectors("+columns+") VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)",
			c.Name, c.Label, c.Transport, c.Command, string(args), string(env), c.URL, c.Local, c.Secret, c.TokenEnv, c.Enabled, c.Outside, by, now, now)
		return err
	})
	if err != nil {
		return Kept{}, err
	}
	return Get(ctx, s, c.Name)
}

// Replace replaces a connector's entry. Its name stays: a connector is
// renamed by removing it and adding it again. What it listed is let go
// when its transport, address or command changed, since another server
// may be there now.
func Replace(ctx context.Context, s *tenant.Store, c Connector) (Kept, error) {
	c = normal(c)
	if err := Check(c); err != nil {
		return Kept{}, err
	}
	old, err := Get(ctx, s, c.Name)
	if err != nil {
		return Kept{}, err
	}
	args, _ := json.Marshal(c.Args)
	env, _ := json.Marshal(c.Env)
	moved := old.Transport != c.Transport || old.URL != c.URL || old.Local != c.Local || old.Command != c.Command || !slices.Equal(old.Args, c.Args)
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE connectors SET label = ?, transport = ?, command = ?, args = ?, env = ?, url = ?, local = ?, secret = ?, token_env = ?,
			enabled = ?, outside = ?, updated_at = ? WHERE name = ?`,
			c.Label, c.Transport, c.Command, string(args), string(env), c.URL, c.Local, c.Secret, c.TokenEnv, c.Enabled, c.Outside, stamp(), c.Name); err != nil {
			return err
		}
		if !moved {
			return nil
		}
		if err := forgetPolicies(ctx, tx, old); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM connector_tools WHERE connector = ?", c.Name); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE connectors SET listed_at = '', problem = '' WHERE name = ?", c.Name)
		return err
	})
	if err != nil {
		return Kept{}, err
	}
	return Get(ctx, s, c.Name)
}

// Remove removes a connector, the tools it listed, its switches in every
// chat, and the person's policies for its tools: a connector added later
// under the same name may be another server, and must not run a tool
// without asking because this one could. The token stays in the vault.
func Remove(ctx context.Context, s *tenant.Store, name string) error {
	old, err := Get(ctx, s, name)
	if err != nil {
		return err
	}
	return inTx(ctx, s, func(tx *sql.Tx) error {
		if err := forgetPolicies(ctx, tx, old); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM connectors WHERE name = ?", name)
		return err
	})
}

// forgetPolicies removes the person's policies for a connector's tools.
// tool_policies is internal/turn's table (P7-02); a policy is kept by the
// tool's name, so this is the one place a connector's change reaches it.
func forgetPolicies(ctx context.Context, tx *sql.Tx, k Kept) error {
	for _, t := range k.Tools {
		if _, err := tx.ExecContext(ctx, "DELETE FROM tool_policies WHERE tool = ?", t.Name); err != nil {
			return err
		}
	}
	return nil
}

// SetEnabled switches a connector on or off for every chat.
func SetEnabled(ctx context.Context, s *tenant.Store, name string, on bool) error {
	res, err := s.DB().ExecContext(ctx, "UPDATE connectors SET enabled = ?, updated_at = ? WHERE name = ?", on, stamp(), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveTools keeps what a connector listed, or why it could not be reached.
// A failure keeps the tools it listed before, so a server that is down for
// a moment does not lose its tools from the screens.
func SaveTools(ctx context.Context, s *tenant.Store, name string, listed []mcp.Tool, problem string, maxTools int) error {
	return inTx(ctx, s, func(tx *sql.Tx) error {
		if problem != "" {
			_, err := tx.ExecContext(ctx, "UPDATE connectors SET problem = ? WHERE name = ?", clip(problem, 600), name)
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM connector_tools WHERE connector = ?", name); err != nil {
			return err
		}
		seen := map[string]bool{}
		pos := 0
		for _, t := range listed {
			if maxTools > 0 && pos >= maxTools {
				break
			}
			if strings.TrimSpace(t.Name) == "" || len(t.Name) > 200 || seen[t.Name] || !schemaObject(t.InputSchema) {
				continue // a tool with no name, a name twice, or no schema of an object cannot be offered
			}
			seen[t.Name] = true
			title := t.Title
			if title == "" && t.Annotations != nil {
				title = t.Annotations.Title
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO connector_tools(connector, position, tool, title, description, schema, read_only) VALUES(?, ?, ?, ?, ?, ?, ?)",
				name, pos, t.Name, clip(title, 200), clip(t.Description, 4000), string(t.InputSchema), t.ReadOnly()); err != nil {
				return err
			}
			pos++
		}
		_, err := tx.ExecContext(ctx, "UPDATE connectors SET listed_at = ?, problem = '' WHERE name = ?", stamp(), name)
		return err
	})
}

// schemaObject reports whether a tool's input schema is a JSON object, as
// a tool's schema must be to be offered. A missing schema is not.
func schemaObject(raw json.RawMessage) bool {
	var v map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &v) == nil && v != nil
}

// Switches returns the person's switch for each connector in one chat. A
// connector with no switch is not in the map, and is on.
func Switches(ctx context.Context, s *tenant.Store, chatID string) (map[string]bool, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT connector, switched_on FROM chat_connectors WHERE chat_id = ?", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var on bool
		if err := rows.Scan(&name, &on); err != nil {
			return nil, err
		}
		out[name] = on
	}
	return out, rows.Err()
}

// SetSwitch switches one connector on or off in one chat.
func SetSwitch(ctx context.Context, s *tenant.Store, chatID, name string, on bool) error {
	_, err := s.DB().ExecContext(ctx, `INSERT INTO chat_connectors(chat_id, connector, switched_on, updated_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(chat_id, connector) DO UPDATE SET switched_on = excluded.switched_on, updated_at = excluded.updated_at`, chatID, name, on, stamp())
	return err
}

func inTx(ctx context.Context, s *tenant.Store, f func(*sql.Tx) error) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// clip keeps the first most characters of a text.
func clip(s string, most int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= most {
		return s
	}
	return string(r[:most-1]) + "…"
}
