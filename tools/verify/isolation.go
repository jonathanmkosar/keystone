package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// checkIsolation is the standing check "tenant isolation", the first of the
// four fixed rules: a tenant never reads another tenant's data.
//
// It makes two tenants in a throwaway data folder, with the shipped
// settings, and tries every way this build has for one to reach the other:
// through the store handle, and through a session. Each try must be refused
// or come back empty. It also checks that each tenant can reach its own
// data, so a check that reaches nothing at all does not pass as isolation.
//
// An item that adds a way to reach tenant data (a route, a tool, a job)
// adds its tries here.
func checkIsolation(string) error {
	dir, err := os.MkdirTemp("", "keystone-isolation-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.Open(dir)
	if err != nil {
		return err
	}
	open := func(name string) (*tenant.Store, error) {
		t, err := in.Create(name)
		if err != nil {
			return nil, err
		}
		return in.Store(t.ID)
	}
	one, err := open("Test Person One")
	if err != nil {
		return err
	}
	defer one.Close()
	two, err := open("Test Person Two")
	if err != nil {
		return err
	}
	defer two.Close()

	var reached []string
	for _, probe := range []func(in *tenant.Install, one, two *tenant.Store) ([]string, error){
		storeProbes,
		sessionProbes,
		routeProbes, // in routes.go
	} {
		got, err := probe(in, one, two)
		if err != nil {
			return fmt.Errorf("the isolation check could not run: %w", err)
		}
		reached = append(reached, got...)
	}
	if len(reached) > 0 {
		return fmt.Errorf("%s", strings.Join(reached, "\n"))
	}
	return nil
}

// storeProbes tries to reach tenant one's rows and files through tenant
// two's store handle, and to open a handle by something that is not an id.
func storeProbes(in *tenant.Install, one, two *tenant.Store) (reached []string, err error) {
	if one.TenantID() == two.TenantID() {
		return nil, errors.New("two tenants were given the same id")
	}

	// Rows.
	if _, err := one.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES('isolation_note', 'only one')"); err != nil {
		return nil, err
	}
	var got string
	if err := one.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'isolation_note'").Scan(&got); err != nil {
		return nil, fmt.Errorf("tenant one cannot read its own row: %w", err)
	}
	if err := two.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'isolation_note'").Scan(&got); err == nil {
		reached = append(reached, "tenant two's store read a row from tenant one's database")
	}

	// Files.
	f, err := one.Files().Create("note.txt")
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(f, "only one"); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if f, err := one.Files().Open("note.txt"); err != nil {
		return nil, fmt.Errorf("tenant one cannot open its own file: %w", err)
	} else {
		f.Close()
	}
	for _, p := range []string{
		"note.txt",
		"../../" + one.TenantID() + "/files/note.txt",
		filepath.Join("..", "..", one.TenantID(), "files", "note.txt"),
		"../../" + one.TenantID() + "/keystone.db",
		filepath.Join(filepath.Dir(one.Files().Name()), "files", "note.txt"),
	} {
		for name, root := range map[string]*os.Root{"files": two.Files(), "artifacts": two.Artifacts()} {
			if f, err := root.Open(p); err == nil {
				f.Close()
				reached = append(reached, fmt.Sprintf("tenant two's %s folder opened tenant one's file through %s", name, p))
			}
		}
	}

	// A handle is opened by an id in the registry and by nothing else.
	for _, id := range []string{
		"../" + one.TenantID(),
		one.TenantID() + "/",
		one.TenantID() + "/../" + one.TenantID(),
		strings.ToUpper(one.TenantID()),
		"..",
		"",
	} {
		if id == one.TenantID() {
			continue // an id with no letters is its own upper case
		}
		if s, err := in.Store(id); err == nil {
			s.Close()
			reached = append(reached, fmt.Sprintf("a store handle was opened for %q, which is not a tenant id", id))
		}
	}
	return reached, nil
}

// sessionProbes signs tenant one in and tries to use that session, and
// tenant one's password, on tenant two.
func sessionProbes(in *tenant.Install, one, two *tenant.Store) (reached []string, err error) {
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	// Invented passwords, used in the throwaway folder only.
	const passwordOne, passwordTwo = "test person one's phrase", "test person two's phrase"
	if err := signin.SetPassword(one, passwordOne, o); err != nil {
		return nil, err
	}
	if err := signin.SetPassword(two, passwordTwo, o); err != nil {
		return nil, err
	}
	token, _, err := signin.SignIn(one, passwordOne, o)
	if err != nil {
		return nil, fmt.Errorf("tenant one cannot sign in to its own tenant: %w", err)
	}
	if _, err := signin.Check(one, token); err != nil {
		return nil, fmt.Errorf("tenant one's session is refused on its own store: %w", err)
	}
	// resolves reports which tenant a token reaches through the API's path.
	resolves := func(token string) (string, error) {
		s, _, err := signin.Resolve(in, token)
		if err != nil {
			return "", err
		}
		defer s.Close()
		return s.TenantID(), nil
	}
	if id, err := resolves(token); err != nil || id != one.TenantID() {
		return nil, fmt.Errorf("tenant one's session does not resolve to tenant one: %q %v", id, err)
	}

	if _, err := signin.Check(two, token); !errors.Is(err, signin.ErrNoSession) {
		reached = append(reached, fmt.Sprintf("tenant one's session was accepted on tenant two's store (%v)", err))
	}

	// The same token with tenant two's id written into it.
	forged := strings.Replace(token, one.TenantID(), two.TenantID(), 1)
	if forged == token {
		return nil, errors.New("the session token does not name its tenant, so the re-addressed token could not be made")
	}
	if _, err := signin.Check(two, forged); !errors.Is(err, signin.ErrNoSession) {
		reached = append(reached, fmt.Sprintf("a session token re-addressed to tenant two was accepted on its store (%v)", err))
	}
	if id, err := resolves(forged); !errors.Is(err, signin.ErrNoSession) {
		reached = append(reached, fmt.Sprintf("a session token re-addressed to tenant two resolved to %q (%v)", id, err))
	}

	// Tenant one's session row copied into tenant two's database, as a
	// mistaken restore or a bug elsewhere might do.
	var id, method, created, expires string
	var hash []byte
	if err := one.DB().QueryRow("SELECT id, token_hash, method, created_at, expires_at FROM signin_sessions").
		Scan(&id, &hash, &method, &created, &expires); err != nil {
		return nil, err
	}
	if _, err := two.DB().Exec("INSERT INTO signin_sessions(id, token_hash, method, created_at, expires_at) VALUES(?, ?, ?, ?, ?)",
		id, hash, method, created, expires); err != nil {
		return nil, err
	}
	for name, tok := range map[string]string{"tenant one's token": token, "a re-addressed token": forged} {
		if _, err := signin.Check(two, tok); !errors.Is(err, signin.ErrNoSession) {
			reached = append(reached, fmt.Sprintf("with tenant one's session row copied into tenant two's database, %s was accepted on tenant two's store (%v)", name, err))
		}
	}
	if id, err := resolves(token); err != nil || id != one.TenantID() {
		reached = append(reached, fmt.Sprintf("with a copied session row, tenant one's token resolved to %q (%v)", id, err))
	}
	if id, err := resolves(forged); !errors.Is(err, signin.ErrNoSession) {
		reached = append(reached, fmt.Sprintf("with a copied session row, a re-addressed token resolved to %q (%v)", id, err))
	}

	// Signing out, and ending every session, through the wrong store.
	if err := signin.SignOut(two, token); !errors.Is(err, signin.ErrNoSession) {
		reached = append(reached, fmt.Sprintf("tenant one's session was signed out through tenant two's store (%v)", err))
	}
	if err := signin.SignOutAll(two); err != nil {
		return nil, err
	}
	if _, err := signin.Check(one, token); err != nil {
		reached = append(reached, fmt.Sprintf("ending tenant two's sessions ended tenant one's (%v)", err))
	}

	// One tenant's password on the other.
	if _, _, err := signin.SignIn(two, passwordOne, o); !errors.Is(err, signin.ErrRefused) {
		reached = append(reached, fmt.Sprintf("tenant one's password signed in to tenant two (%v)", err))
	}

	// Once signed out, the token reaches nothing.
	if err := signin.SignOut(one, token); err != nil {
		return nil, err
	}
	if id, err := resolves(token); !errors.Is(err, signin.ErrNoSession) {
		reached = append(reached, fmt.Sprintf("a signed-out session still resolved to %q (%v)", id, err))
	}
	return reached, nil
}
