package loadtest

import (
	"strings"
	"testing"
)

// The small profile, as the standing check runs it: the generator, every
// measurement inside its budget, and the Monitor's figures equal to the
// test's own.
func TestTheSmallLoadStaysInsideItsBudgets(t *testing.T) {
	p, err := SmallProfile()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(p, Options{Dir: t.TempDir(), Say: func(s string) { t.Log(s) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + Table(res))
	if len(res.Problems) > 0 {
		t.Fatalf("the small load did not pass:\n%s", strings.Join(res.Problems, "\n"))
	}
	if len(res.Measurements) != len(Measures) {
		t.Fatalf("%d measurements were made, want %d", len(res.Measurements), len(Measures))
	}
	for _, m := range res.Measurements {
		if m.Runs < p.Runs || m.Median <= 0 || m.P95 < m.Median || m.Budget <= 0 {
			t.Errorf("%s: %d runs, median %s, 95th percentile %s, budget %s", m.Name, m.Runs, m.Median, m.P95, m.Budget)
		}
	}
	s := res.Sizes
	if s.Chats != p.Chats+1 || s.Turns < p.Chats+p.LongChatTurns || s.Messages != 2*s.Turns || s.DatabaseBytes <= 0 || len(s.Tables) == 0 {
		t.Errorf("the sizes do not hold the load: %+v", s)
	}
	if s.Uploads == 0 || s.Artifacts == 0 || s.FilesBytes == 0 || s.ArtifactsBytes == 0 {
		t.Errorf("the load has %d uploads (%d bytes) and %d artifacts (%d bytes)", s.Uploads, s.FilesBytes, s.Artifacts, s.ArtifactsBytes)
	}
	if len(res.Monitor) < 12 {
		t.Errorf("the Monitor was compared on %d figures", len(res.Monitor))
	}
}
