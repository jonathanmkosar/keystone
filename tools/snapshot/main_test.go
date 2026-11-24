package main

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotIgnoresLineEndingsAndBuildOutput(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "go.mod", "module x\n")
	write(t, a, "docs/a.md", "one\ntwo\n")
	write(t, b, "go.mod", "module x\r\n")
	write(t, b, "docs/a.md", "one\r\ntwo\r\n")
	write(t, b, "bin/keystone.exe", "built")
	write(t, b, "data/tenants/x/keystone.db", "private")
	ha, na, err := Snapshot(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, nb, err := Snapshot(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb || na != 2 || nb != 2 {
		t.Errorf("want the same hash over 2 files, got %s (%d) and %s (%d)", ha, na, hb, nb)
	}
	write(t, b, "docs/a.md", "one\ntwo\nthree\n")
	hc, _, _ := Snapshot(b)
	if hc == ha {
		t.Error("a changed file did not change the hash")
	}
}
