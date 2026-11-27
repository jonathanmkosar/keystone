// Command loadtest runs the load test (P13-03): years of invented chats for
// one person and a smaller load for several more, in a throwaway data
// folder, with the times a person waits on measured against the real
// server and held to their budgets, and the Monitor's figures compared
// with the test's own counts. It calls no model and needs no network.
//
//	go run ./tools/loadtest
//
// The full run takes minutes and several gigabytes of disk for its data
// folder, which is removed when it ends. It prints its table and writes it
// into docs/architecture/load-test.md and the item's Result. The profile
// and the budgets are settings, in settings/loadtest.json.
//
//	go run ./tools/loadtest -small          the profile verify runs, in seconds
//	go run ./tools/loadtest -chats 500      another size, to try one; nothing is written to the docs
//	go run ./tools/loadtest -dir D:\scratch where the data folder is made
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jonathanmkosar/keystone/internal/loadtest"
	"github.com/jonathanmkosar/keystone/internal/project"
)

// The files the full run's table is written into.
var written = []string{
	"docs/architecture/load-test.md",
	"docs/plan/items/P13-03-load-test.md",
}

func main() {
	small := flag.Bool("small", false, "run the small profile, the one verify runs")
	chats := flag.Int("chats", 0, "the first person's chats, in place of the profile's")
	turns := flag.Int("turns", 0, "the mean turns in a chat, in place of the profile's")
	long := flag.Int("long", 0, "the long chat's turns, in place of the profile's")
	tenants := flag.Int("tenants", 0, "how many more people, in place of the profile's")
	tenantChats := flag.Int("tenant-chats", 0, "each other person's chats, in place of the profile's")
	runs := flag.Int("runs", 0, "how many times each measurement is made, in place of the profile's")
	dir := flag.String("dir", "", "the folder the throwaway data folder is made in (the system's temporary folder when empty)")
	keep := flag.Bool("no-write", false, "print the table and write it nowhere")
	flag.Parse()

	root, err := project.Root()
	if err != nil {
		fail(err)
	}
	read := loadtest.FullProfile
	if *small {
		read = loadtest.SmallProfile
	}
	p, err := read()
	if err != nil {
		fail(err)
	}
	changed := false
	for _, o := range []struct {
		flag *int
		into *int
	}{{chats, &p.Chats}, {turns, &p.TurnsPerChat}, {long, &p.LongChatTurns}, {tenants, &p.Tenants}, {tenantChats, &p.TenantChats}, {runs, &p.Runs}} {
		if *o.flag > 0 {
			*o.into, changed = *o.flag, true
		}
	}
	if changed {
		p.Name += ", with sizes of its own"
	}

	began := time.Now()
	res, err := loadtest.Run(p, loadtest.Options{Dir: *dir, Say: func(s string) {
		fmt.Printf("%6s  %s\n", time.Since(began).Round(time.Second), s)
	}})
	if err != nil {
		fail(err)
	}
	fmt.Println()
	fmt.Println(loadtest.Table(res))

	// Only the full run, as the settings give it, is the docs' table.
	if !*small && !changed && !*keep {
		for _, f := range written {
			if err := loadtest.WriteTable(filepath.Join(root, filepath.FromSlash(f)), res); err != nil {
				fail(err)
			}
			fmt.Println("written to", f)
		}
	}
	if len(res.Problems) > 0 {
		fmt.Fprintf(os.Stderr, "\nthe load test did not pass: %d problems\n", len(res.Problems))
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "loadtest:", err)
	os.Exit(1)
}
