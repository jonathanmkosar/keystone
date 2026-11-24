// Command snapshot prints one hash for the source tree, so two checkouts can
// be compared. Build output, data and git's own folder are left out, and line
// endings are ignored, so Windows and Linux agree.
//
//	go run ./tools/snapshot
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/jonathanmkosar/keystone/internal/project"
)

func main() {
	root, err := project.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hash, n, err := Snapshot(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s  %d files\n", hash, n)
}

// Snapshot hashes every file under root, by path and content.
func Snapshot(root string) (string, int, error) {
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if project.Skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Contains(raw, []byte{0}) {
			raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		}
		rel, _ := filepath.Rel(root, p)
		lines = append(lines, fmt.Sprintf("%x  %s", sha256.Sum256(raw), filepath.ToSlash(rel)))
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		fmt.Fprintln(h, l)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16], len(lines), nil
}
