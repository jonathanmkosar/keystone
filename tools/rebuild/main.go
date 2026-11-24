// Command rebuild takes a fresh checkout to a built, checked Keystone.
// It can be run twice and gives the same result.
//
//	go run ./tools/rebuild
//
// Today it checks the tools, builds the binary into bin/, runs it once, and
// runs verify. Creating the data folder and loading the test tenant are added
// by the items that build them (P2-01 and P3-09).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/project"
)

type step struct {
	name string
	run  func(root string) (string, error)
}

func main() {
	root, err := project.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(root, "bin", "keystone")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	steps := []step{
		{"Go is installed", func(r string) (string, error) { return project.Run(r, "go", "version") }},
		{"build bin/" + filepath.Base(bin), func(r string) (string, error) {
			if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
				return "", err
			}
			return project.Run(r, "go", "build", "-o", bin, "./cmd/keystone")
		}},
		{"the binary runs", func(r string) (string, error) {
			out, err := project.Run(r, bin, "settings")
			if err == nil {
				lines := strings.Split(out, "\n")
				out = lines[len(lines)-1]
			}
			return out, err
		}},
		{"verify", func(r string) (string, error) {
			out, err := project.Run(r, "go", "run", "./tools/verify")
			if err == nil {
				lines := strings.Split(out, "\n")
				out = lines[len(lines)-1]
			}
			return out, err
		}},
		{"snapshot", func(r string) (string, error) { return project.Run(r, "go", "run", "./tools/snapshot") }},
	}
	start := time.Now()
	for _, s := range steps {
		out, err := s.run(root)
		if err != nil {
			fmt.Printf("FAIL  %s\n%s\n%v\n", s.name, out, err)
			os.Exit(1)
		}
		fmt.Printf("ok    %-22s %s\n", s.name, out)
	}
	fmt.Printf("\nRebuilt in %s. Run bin/%s to start.\n", time.Since(start).Round(time.Millisecond), filepath.Base(bin))
}
