// Package project is shared by the project scripts in tools/: finding the
// repo root, running a command in it, and reading the plan's work items and
// STATE.md. Nothing in the Keystone binary uses it.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Root walks up from the working directory to the folder that holds go.mod.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod found here or in any folder above: run this from inside the Keystone repo")
		}
		dir = parent
	}
}

// Run runs a command in dir and returns everything it printed, trimmed.
func Run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return strings.TrimSpace(buf.String()), err
}

// Item is one work item file from docs/plan/items.
type Item struct {
	ID      string
	Title   string
	Status  string
	Lane    string
	Depends []string
	Gate    string
	File    string
}

var (
	idShape   = regexp.MustCompile(`^P(\d+)-(\d+)$`)
	headShape = regexp.MustCompile(`^# (P\d+-\d+)\. (.+)$`)
)

// ParseItem reads the header lines of one item file.
func ParseItem(name, text string) (Item, error) {
	it := Item{File: name}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if m := headShape.FindStringSubmatch(lines[0]); m != nil {
		it.ID, it.Title = m[1], m[2]
	} else {
		return it, fmt.Errorf("%s: the first line is not \"# P0-01. Title\"", name)
	}
	for _, l := range lines[1:] {
		key, val, ok := strings.Cut(l, ": ")
		if !ok {
			if strings.HasPrefix(l, "## ") {
				break
			}
			continue
		}
		switch key {
		case "Status":
			it.Status = val
		case "Lane":
			it.Lane = val
		case "Owner gate":
			it.Gate = val
		case "Depends on":
			if val != "nothing" {
				for _, d := range strings.Split(val, ",") {
					it.Depends = append(it.Depends, strings.TrimSpace(d))
				}
			}
		}
	}
	if it.Status == "" || it.Lane == "" {
		return it, fmt.Errorf("%s: an item needs a Status line and a Lane line", name)
	}
	return it, nil
}

// Items reads every work item under docs/plan/items, in plan order.
func Items(root string) ([]Item, error) {
	dir := filepath.Join(root, "docs", "plan", "items")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		it, err := ParseItem(e.Name(), string(raw))
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return order(items[i].ID) < order(items[j].ID) })
	return items, nil
}

func order(id string) int {
	m := idShape.FindStringSubmatch(id)
	if m == nil {
		return 1 << 30
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a*1000 + b
}

// Done reports whether a status counts as finished for the items that depend on it.
func Done(status string) bool {
	return status == "done"
}

// Open reports whether an item can still be picked: it is todo or provisional.
func Open(status string) bool {
	return status == "todo" || strings.HasPrefix(status, "provisional")
}

// CheckItems reports every problem in the plan's items: a dependency that
// does not exist, a loop, or a status the loop does not know.
func CheckItems(items []Item) []string {
	var problems []string
	byID := map[string]Item{}
	for _, it := range items {
		if _, dup := byID[it.ID]; dup {
			problems = append(problems, it.ID+" is defined twice")
		}
		byID[it.ID] = it
	}
	for _, it := range items {
		s := it.Status
		if !(s == "todo" || s == "done" || strings.HasPrefix(s, "doing") || strings.HasPrefix(s, "provisional")) {
			problems = append(problems, fmt.Sprintf("%s has status %q, which is not todo, doing, provisional or done", it.ID, s))
		}
		for _, d := range it.Depends {
			if _, ok := byID[d]; !ok {
				problems = append(problems, fmt.Sprintf("%s depends on %s, which does not exist", it.ID, d))
			}
		}
	}
	state := map[string]int{}
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, d := range byID[id].Depends {
			if _, ok := byID[d]; ok && visit(d) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for _, it := range items {
		if visit(it.ID) {
			problems = append(problems, it.ID+" is part of a loop of dependencies")
			break
		}
	}
	return problems
}

// Ready lists the open items whose dependencies are all done, in plan order.
func Ready(items []Item) []Item {
	done := map[string]bool{}
	for _, it := range items {
		done[it.ID] = Done(it.Status)
	}
	var ready []Item
next:
	for _, it := range items {
		if !Open(it.Status) {
			continue
		}
		for _, d := range it.Depends {
			if !done[d] {
				continue next
			}
		}
		ready = append(ready, it)
	}
	return ready
}

// StateLine returns the line of STATE.md that starts with key, such as "Phase:".
func StateLine(state, key string) string {
	for _, l := range strings.Split(strings.ReplaceAll(state, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, key) {
			return l
		}
	}
	return key + " (missing from STATE.md)"
}

// Skip reports whether a folder is left out when the project scripts walk the tree.
func Skip(name string) bool {
	switch name {
	case ".git", "bin", "data", "node_modules":
		return true
	}
	return false
}
