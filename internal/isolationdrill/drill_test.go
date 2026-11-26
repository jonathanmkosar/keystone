package isolationdrill

import (
	"strings"
	"testing"
)

// TestTheSecondPersonReachesNothing is the drill, P13-02's proof: every way
// the second person tries is refused or answered with nothing of the first
// person's, the first person's folder, database and audit log do not move,
// and every try is in the second person's own audit log. Run it with -v to
// see each try and its result.
func TestTheSecondPersonReachesNothing(t *testing.T) {
	r, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	areas := map[string]int{}
	for _, a := range r.Attempts {
		areas[a.Area]++
		t.Logf("%-16s %s\n%18s-> %s", a.Area, a.Tried, "", a.Result)
	}
	for _, area := range []string{AreaAPI, AreaTools, AreaJobs, AreaDisk, AreaAudit} {
		if areas[area] == 0 {
			t.Errorf("the drill tried nothing in its part %q", area)
		}
	}
	t.Logf("%d tries: %v", len(r.Attempts), areas)
	for _, n := range r.Notes {
		t.Logf("note: %s", n)
	}
	if len(r.Reached) > 0 {
		t.Errorf("the second person reached the first person's data, or a try went unrecorded:\n%s", strings.Join(r.Reached, "\n"))
	}
}
