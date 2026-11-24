// Command rebuild takes a fresh checkout to a built, checked Keystone.
// It can be run twice and gives the same result.
//
//	go run ./tools/rebuild
//
// Today it checks the tools, builds the binary into bin/, runs it once,
// starts it and fetches the embedded page over HTTP, and runs verify. Creating the data folder and loading the test tenant are added
// by the items that build them (P2-01 and P3-09).
package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
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
		{"it serves the page", func(string) (string, error) { return servesThePage(bin) }},
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
	fmt.Printf("\nRebuilt in %s. Run bin/%s serve to start.\n", time.Since(start).Round(time.Millisecond), filepath.Base(bin))
}

// servesThePage starts the built binary on a free port on this machine,
// fetches the embedded page over HTTP, and stops the binary again.
func servesThePage(bin string) (string, error) {
	cmd := exec.Command(bin, "serve", "--address", "127.0.0.1:0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		first <- line
	}()
	var line string
	select {
	case line = <-first:
	case <-time.After(15 * time.Second):
		return "", fmt.Errorf("the binary did not say where it listens within 15 seconds")
	}
	i := strings.Index(line, "http://")
	if i < 0 {
		return line, fmt.Errorf("the binary did not say where it listens")
	}
	url := strings.TrimSpace(line[i:]) + "/"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>Keystone</title>") {
		return "", fmt.Errorf("%s answered %d and not the Keystone page", url, resp.StatusCode)
	}
	return fmt.Sprintf("%d bytes over HTTP", len(body)), nil
}
