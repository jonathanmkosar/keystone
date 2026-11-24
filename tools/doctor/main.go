// Command doctor says where the work stands: the state, whether the build is
// healthy, and the next item each lane can pick up.
//
//	go run ./tools/doctor
package main

import (
	"fmt"
	"os"
	"path/filepath"
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
	if sick {
		os.Exit(1)
	}
}
