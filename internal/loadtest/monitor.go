package loadtest

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/operations"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// MonitorLine is one figure Operations reported for the loaded person,
// beside the test's own count of it.
type MonitorLine struct {
	// When is "after the load is written" or "after the measurements".
	When   string `json:"when"`
	Figure string `json:"figure"`
	// Reported is Operations' figure, and Own the test's.
	Reported string `json:"reported"`
	Own      string `json:"own"`
	Match    bool   `json:"match"`
}

// The two moments the Monitor is compared at.
const (
	// WhenLoaded is after the generator, before any measured turn: every
	// call but the probe's is one the generator wrote, with a time of its
	// own, so each model's calls and median latency are known exactly.
	WhenLoaded = "after the load is written"
	// WhenMeasured is after the measurements: the newest traces are the
	// test's own turns, and the cap holds their spend.
	WhenMeasured = "after the measurements"
)

// databaseBytes is the size of a person's database file and its
// write-ahead log, as the test reads them from the folder.
func (g *rig) databaseBytes(who *person) (int64, error) {
	file := filepath.Join(g.dir, "tenants", who.id, "keystone.db")
	info, err := os.Stat(file)
	if err != nil {
		return 0, err
	}
	n := info.Size()
	if wal, err := os.Stat(file + "-wal"); err == nil {
		n += wal.Size()
	}
	return n, nil
}

// monitor reads the first person's Operations report through the API and
// sets each figure beside the test's own count. The test's counts come
// from what the generator wrote and what the rig's own turns streamed,
// never from the database the report reads.
func (g *rig) monitor(when string) ([]MonitorLine, error) {
	who := g.people[0]
	gen := who.generated
	var lines []MonitorLine
	add := func(figure, reported, own string, match bool) {
		lines = append(lines, MonitorLine{When: when, Figure: figure, Reported: reported, Own: own, Match: match})
	}
	n := func(v int64) string { return strconv.FormatInt(v, 10) }

	// The report's own call adds its two audit entries, so the database is
	// read just before and just after, and the report's figure must lie
	// between.
	before, err := g.databaseBytes(who)
	if err != nil {
		return nil, err
	}
	var rep operations.Report
	if err := g.as(who, "GET", "operations", nil, http.StatusOK, &rep); err != nil {
		return nil, err
	}
	after, err := g.databaseBytes(who)
	if err != nil {
		return nil, err
	}
	limits := map[string]operations.Limit{}
	for _, l := range rep.Limits {
		limits[l.Key] = l
	}
	db := limits[operations.LimitDatabase]
	add("database, bytes", n(db.Used), n(before)+" to "+n(after), db.Problem == "" && db.Used >= before && db.Used <= after)

	g.mu.Lock()
	extra := g.extraFileBytes
	real := append([]realCall(nil), g.real[who.id]...)
	loose := map[string]bool{}
	for id := range g.loose {
		loose[id] = true
	}
	g.mu.Unlock()
	files := limits[operations.LimitFiles]
	add("files, bytes", n(files.Used), n(gen.fileBytes+extra), files.Problem == "" && files.Used == gen.fileBytes+extra)
	arts := limits[operations.LimitArtifacts]
	add("artifacts, bytes", n(arts.Used), n(gen.artifactBytes), arts.Problem == "" && arts.Used == gen.artifactBytes)

	// The cap: this month's spend on models that are not local.
	month := usage.MonthOf(rep.At)
	var spent int64
	for _, c := range gen.calls {
		if c.class != models.ClassLocal && usage.MonthOf(c.at) == month {
			spent += c.costMicros
		}
	}
	for _, c := range real {
		if c.model == hostedModel.Name {
			spent += c.costMicros
		}
	}
	capOf := int64(0)
	if st, ok := settings.Lookup(g.reg, "usage.monthly_cap_usd"); ok {
		usd, _ := strconv.ParseFloat(st.Default, 64)
		capOf = int64(usd * 1e6)
	}
	limit := limits[operations.LimitCap]
	add("the cap, spent this month, millionths of a dollar", n(limit.Used), n(spent), limit.Problem == "" && limit.Used == spent)
	add("the cap, millionths of a dollar", n(limit.Of), n(capOf), limit.Of == capOf)

	// Each model's calls over the report's window.
	since := rep.Models.Since
	for _, m := range []models.Model{hostedModel, localModel} {
		var calls int
		var cost int64
		var times []int64
		for _, c := range gen.calls {
			if c.model == m.Name && !c.at.Before(since) {
				calls++
				cost += c.costMicros
				times = append(times, c.durationMS)
			}
		}
		realCalls := 0
		for _, c := range real {
			if c.model == m.Name {
				realCalls++
				cost += c.costMicros
			}
		}
		var got operations.ModelHealth
		for _, h := range rep.Models.Models {
			if h.Name == m.Name {
				got = h
			}
		}
		// A turn in the long chat sets off a summary of its oldest turns,
		// beside the turn, which the stream does not tell: the local
		// model's calls are then at least the test's count.
		atLeast := m.Name == localModel.Name && len(loose) > 0
		figure, own := "calls to "+m.Name+" in the last "+rep.Models.Window, strconv.Itoa(calls+realCalls)
		if atLeast {
			add(figure, strconv.Itoa(got.Calls), own+" or more", got.Calls >= calls+realCalls && got.OK == got.Calls)
		} else {
			add(figure, strconv.Itoa(got.Calls), own, got.Calls == calls+realCalls && got.OK == got.Calls)
			add("cost of "+m.Name+" in the last "+rep.Models.Window+", millionths of a dollar", n(got.CostMicros), n(cost), got.CostMicros == cost)
		}
		// The median latency is the generator's own times, while no call of
		// the model's is one the server timed.
		if realCalls == 0 && calls > 0 {
			own := medianOf(times)
			add("median latency of "+m.Name+", milliseconds", n(got.MedianMS), n(own), got.MedianMS == own)
		}
	}

	// The traces: the newest turns, each with the calls and tokens the test
	// counted for its request.
	ownTraces := map[string]ownTrace{}
	for _, c := range real {
		t := ownTraces[c.requestID]
		t.calls++
		t.input, t.output, t.cost = t.input+int64(c.usage.InputTokens), t.output+int64(c.usage.OutputTokens), t.cost+c.costMicros
		ownTraces[c.requestID] = t
	}
	matched, unknown, wrong := 0, 0, ""
	for _, tr := range rep.Traces {
		own, ok := ownTraces[tr.RequestID]
		if !ok {
			if t := gen.traces[tr.RequestID]; t != nil {
				own, ok = *t, true
			}
		}
		got := ownTrace{calls: tr.Calls, input: tr.Usage.InputTokens, output: tr.Usage.OutputTokens, cost: tr.Usage.CostMicros}
		switch {
		case !ok:
			unknown++
		case got == own, loose[tr.RequestID] && got.calls >= own.calls && got.input >= own.input:
			matched++
		case wrong == "":
			wrong = fmt.Sprintf("; request %s: reported %+v, own %+v", tr.RequestID, got, own)
		}
	}
	want := len(rep.Traces)
	add("traces whose calls, tokens and cost are the test's own", strconv.Itoa(matched)+" of "+strconv.Itoa(want)+wrong,
		strconv.Itoa(want)+" of "+strconv.Itoa(want), want > 0 && matched == want && unknown == 0)
	if when == WhenMeasured && len(rep.Traces) > 0 {
		newest := ""
		if len(real) > 0 {
			newest = real[len(real)-1].requestID
		}
		// The last call noted is the first person's last turn: every person
		// sends at once, each with a request of their own.
		add("the newest trace", rep.Traces[0].RequestID, newest, rep.Traces[0].RequestID == newest)
	}
	return lines, nil
}

func medianOf(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	v = append([]int64(nil), v...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/2-1] + v[len(v)/2]) / 2
}
