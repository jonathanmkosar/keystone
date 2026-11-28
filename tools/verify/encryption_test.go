package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scan finds what it is there to find: a plain header, the marker, and
// a plain name.
func TestTheEncryptionScanFindsPlainBytes(t *testing.T) {
	folder := t.TempDir()
	w := written{uploadID: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	for name, body := range map[string]string{
		"keystone.db": "SQLite format 3\x00",
		"a":           "some " + encryptionMarker + " words",
		w.uploadID:    "sealed, but under its plain name",
		"b":           "x", "c": "x", "d": "x",
	} {
		os.WriteFile(filepath.Join(folder, name), []byte(body), 0o600)
	}
	got := strings.Join(scanSealed(folder, w), "\n")
	for _, want := range []string{"plain database header", "marker word", "plain name"} {
		if !strings.Contains(got, want) {
			t.Errorf("the scan did not find a %s:\n%s", want, got)
		}
	}
}

func TestTheEncryptionCheckPasses(t *testing.T) {
	if err := checkEncryption(""); err != nil {
		t.Fatal(err)
	}
}
