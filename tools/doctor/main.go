// Command doctor says where the work stands: the state, whether the build is
// healthy, and the next item each lane can pick up.
//
//	go run ./tools/doctor
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/project"
)

func main() {
	root, err := project.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sick := false

	state, err := os.ReadFile(filepath.Join(root, "docs", "plan", "STATE.md"))
	if err != nil {
		fmt.Println("STATE.md is missing. Restoring it is this loop's item.")
		sick = true
	} else {
		for _, key := range []string{"Phase:", "Next:", "Blocked:", "Hold:"} {
			line := project.StateLine(string(state), key)
			if key == "Hold:" && strings.Contains(line, "(missing") {
				continue
			}
			fmt.Println(line)
		}
	}

	gov, _ := project.Run(root, "go", "version")
	fmt.Println("Go:", gov)
	if out, err := project.Run(root, "go", "build", "./..."); err != nil {
		fmt.Println("Build: BROKEN. Fixing it is this loop's item.")
		fmt.Println(out)
		sick = true
	} else {
		fmt.Println("Build: ok")
	}

	items, err := project.Items(root)
	if err != nil {
		fmt.Println("Items:", err)
		os.Exit(1)
	}
	if p := project.CheckItems(items); len(p) > 0 {
		fmt.Println("Plan: BROKEN")
		for _, line := range p {
			fmt.Println("  " + line)
		}
		sick = true
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[strings.SplitN(it.Status, " ", 2)[0]]++
	}
	fmt.Printf("Items: %d done, %d doing, %d provisional, %d todo, of %d\n",
		counts["done"], counts["doing"], counts["provisional:"]+counts["provisional"], counts["todo"], len(items))

	// The first ready item in each lane. An item with two lanes is listed under its first.
	next := map[string][]project.Item{}
	for _, it := range project.Ready(items) {
		lane := strings.SplitN(it.Lane, " + ", 2)[0]
		next[lane] = append(next[lane], it)
	}
	lanes := make([]string, 0, len(next))
	for l := range next {
		lanes = append(lanes, l)
	}
	sort.Strings(lanes)
	fmt.Println("\nReady now, by lane:")
	if len(lanes) == 0 {
		fmt.Println("  nothing")
	}
	for _, l := range lanes {
		ids := make([]string, 0, len(next[l]))
		for _, it := range next[l] {
			ids = append(ids, it.ID)
		}
		first := next[l][0]
		gate := ""
		if first.Gate != "" && first.Gate != "none" {
			gate = "  (owner gate: build what the item says can be built without it)"
		}
		fmt.Printf("  %-10s %s %s%s\n", l, first.ID, first.Title, gate)
		if len(ids) > 1 {
			fmt.Printf("  %-10s then %s\n", "", strings.Join(ids[1:], ", "))
		}
	}
	inFlight(root, items)
	if sick {
		os.Exit(1)
	}
}

// laneBranch is a lane's branch: the lane, a slash, then the item's id in
// lower case, such as agents/p7-02-approvals.
var laneBranch = regexp.MustCompile(`^[a-z]+/p\d+-\d+`)

// inFlight lists the work a stopped session may have left: every item
// marked doing, and every lane branch not yet merged into the branch this
// checkout is on, with where its work is: commits not merged, commits not
// pushed, and files not committed in its worktree. docs/plan/LOOP.md says
// how to pick one up.
func inFlight(root string, items []project.Item) {
	base, err := project.Run(root, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return
	}
	base = strings.TrimSpace(base)

	// Worktrees, by the branch they have checked out.
	where := map[string]string{}
	if out, err := project.Run(root, "git", "worktree", "list", "--porcelain"); err == nil {
		path := ""
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "worktree "):
				path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch refs/heads/"):
				where[strings.TrimPrefix(line, "branch refs/heads/")] = path
			}
		}
	}

	var branches []string
	if out, err := project.Run(root, "git", "for-each-ref", "--format=%(refname:short)", "refs/heads"); err == nil {
		for _, b := range strings.Fields(out) {
			if !laneBranch.MatchString(b) {
				continue
			}
			if n := count(root, base+".."+b); n > 0 {
				branches = append(branches, b)
			}
		}
	}

	var doing []project.Item
	for _, it := range items {
		if strings.HasPrefix(it.Status, "doing") {
			doing = append(doing, it)
		}
	}
	if len(doing) == 0 && len(branches) == 0 {
		fmt.Println("\nIn flight: nothing.")
		return
	}

	fmt.Printf("\nIn flight (not yet merged into %s):\n", base)
	shown := map[string]bool{}
	describe := func(b string) {
		shown[b] = true
		line := fmt.Sprintf("        branch %s: %d commits not merged", b, count(root, base+".."+b))
		if _, err := project.Run(root, "git", "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+b); err != nil {
			line += ", not pushed"
		} else if n := count(root, "origin/"+b+".."+b); n > 0 {
			line += fmt.Sprintf(", %d not pushed", n)
		} else {
			line += ", pushed"
		}
		fmt.Println(line)
		if path, ok := where[b]; ok {
			dirty := 0
			if out, err := project.Run(path, "git", "status", "--porcelain"); err == nil {
				for _, l := range strings.Split(out, "\n") {
					if strings.TrimSpace(l) != "" {
						dirty++
					}
				}
			}
			state := "clean"
			if dirty > 0 {
				state = fmt.Sprintf("%d files not committed", dirty)
			}
			fmt.Printf("        worktree %s: %s\n", path, state)
		}
	}
	for _, it := range doing {
		fmt.Printf("  %s %s (%s)\n", it.ID, it.Title, it.Status)
		found := false
		for _, b := range branches {
			if strings.HasPrefix(strings.SplitN(b, "/", 2)[1], strings.ToLower(it.ID)+"-") {
				describe(b)
				found = true
			}
		}
		if !found {
			fmt.Println("        no branch with work on it yet")
		}
	}
	for _, b := range branches {
		if !shown[b] {
			fmt.Printf("  %s, for an item not marked doing\n", b)
			describe(b)
		}
	}
	fmt.Println("To pick one up after a stop: \"Resuming after a stop\" in docs/plan/LOOP.md.")
}

// count is the number of commits in a git range, or 0.
func count(root, rangeSpec string) int {
	out, err := project.Run(root, "git", "rev-list", "--count", rangeSpec)
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &n)
	return n
}
