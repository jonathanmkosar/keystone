package importer

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/imported"
)

// Write prints a report for a person. It prints counts, model names, table
// names and dates: never a title, a routine's name or a word of a message,
// so the output of a dry run or an import can be shown to anyone.
func (r Report) Write(w io.Writer) {
	verb, none := "Imported", "Nothing was imported: everything this copy holds that Keystone brings is here already."
	if r.Check {
		verb, none = "Would import", "Nothing would be imported: everything this copy holds that Keystone brings is here already."
	}
	fmt.Fprintf(w, "The copy (SHA-256 %s…) holds %s and %s of this workspace.\n", short(r.FileSHA256), count(r.SourceTurns, "turn", "turns"), count(r.SourceRoutines, "routine", "routines"))
	if r.AlreadyTurns > 0 || r.AlreadyRoutines > 0 {
		fmt.Fprintf(w, "Here already, from an earlier import: %s and %s.\n", count(r.AlreadyTurns, "turn", "turns"), count(r.AlreadyRoutines, "routine", "routines"))
	}
	fmt.Fprintln(w)
	if r.Chats == 0 && r.Schedules == 0 {
		fmt.Fprintln(w, none)
	} else {
		fmt.Fprintf(w, "%s:\n", verb)
		t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintf(t, "  chats\t%d\t%s\n", r.Chats, kinds(r.ChatsByKind))
		fmt.Fprintf(t, "  turns\t%d\t%s\n", r.Turns, unanswered(r.Unanswered))
		fmt.Fprintf(t, "  messages\t%d\t%d from the person, %s\n", r.Messages, r.FromPerson, count(r.Replies, "reply", "replies"))
		if r.First != nil && r.Last != nil {
			fmt.Fprintf(t, "  from\t%s\tto %s (UTC)\n", r.First.UTC().Format("2006-01-02"), r.Last.UTC().Format("2006-01-02"))
		}
		held := "none is held to local or trusted models"
		if r.Held > 0 {
			held = "held to local or trusted models"
		}
		fmt.Fprintf(t, "  held chats\t%d\t%s (%s)\n", r.Held, held, KeyHold)
		brief := ""
		if r.Brief {
			brief = ", the morning brief among them"
		}
		fmt.Fprintf(t, "  schedules\t%d\tfrom routines, every one paused%s\n", r.Schedules, brief)
		t.Flush()
		if len(r.Models) > 0 {
			fmt.Fprintf(w, "\nReplies by the model %s named:\n", imported.Label(imported.NewKeystone))
			t = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			var names []string
			for name := range r.Models {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				shown := name
				if shown == "" {
					shown = "(none named)"
				}
				fmt.Fprintf(t, "  %s\t%d\n", shown, r.Models[name])
			}
			t.Flush()
		}
	}
	switch {
	case r.ZoneSet != "" && r.Check:
		fmt.Fprintf(w, "\nThis person has no time zone of their own, so agents.time_zone would be set to %s, the zone New Keystone kept its routines' times in.\n", r.ZoneSet)
	case r.ZoneSet != "":
		fmt.Fprintf(w, "\nThis person had no time zone of their own, so agents.time_zone is now %s, the zone New Keystone kept its routines' times in.\n", r.ZoneSet)
	case r.ZoneKept != "":
		fmt.Fprintf(w, "\nThis person's time zone is %s and New Keystone kept its routines' times in %s. Each schedule keeps the time on the clock: 07:00 there is 07:00 in %s here.\n", r.ZoneKept, r.ZoneWas, r.ZoneKept)
	}
	if len(r.Left) > 0 {
		fmt.Fprintln(w, "\nLeft out:")
		t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, l := range r.Left {
			fmt.Fprintf(t, "  %d\t%s\n", l.Count, l.What)
		}
		t.Flush()
	}
	if len(r.Tables) > 0 {
		fmt.Fprintln(w, "\nThe copy's other tables, by rows of this workspace:")
		t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, l := range r.Tables {
			fmt.Fprintf(t, "  %d\t%s\n", l.Count, l.What)
		}
		t.Flush()
	}
	if len(r.Memories) > 0 {
		var states []string
		for state := range r.Memories {
			states = append(states, state)
		}
		sort.Strings(states)
		for i, state := range states {
			states[i] = fmt.Sprintf("%s %d", state, r.Memories[state])
		}
		fmt.Fprintf(w, "\nNotebook entries by state: %s.\n", strings.Join(states, ", "))
	}
	switch {
	case r.Check:
		fmt.Fprintln(w, "\nNothing was written. No message, title or name is printed, here or by the import.")
	case r.ImportID != "":
		fmt.Fprintf(w, "\nThe import is in this person's audit log as %s, under the request id import-%s. No model was called.\n", Action, r.ImportID)
	}
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func unanswered(n int) string {
	switch n {
	case 0:
		return "each with its reply"
	case 1:
		return "1 with no reply (it failed or was cancelled in New Keystone)"
	}
	return fmt.Sprintf("%d with no reply (they failed or were cancelled in New Keystone)", n)
}

// kinds says how many chats of each kind there are.
func kinds(by map[string]int) string {
	var parts []string
	for _, k := range []struct{ kind, one, many string }{
		{imported.ChatStretch, "stretch of the continuing chat", "stretches of the continuing chat"},
		{imported.ChatRoutine, "routine run", "routine runs"},
		{imported.ChatBuild, "build", "builds"},
	} {
		if n := by[k.kind]; n > 0 {
			parts = append(parts, count(n, k.one, k.many))
		}
	}
	return strings.Join(parts, ", ")
}
