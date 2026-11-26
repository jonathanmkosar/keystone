package carry

import "testing"

// A sub-agent's chat starts with every mark of its parent's.
func TestDownCarriesEveryMark(t *testing.T) {
	for _, m := range []Marks{{}, {Outside: true}, {Sensitive: true}, {Outside: true, Sensitive: true}} {
		if Down(m) != m {
			t.Errorf("Down(%+v) = %+v", m, Down(m))
		}
	}
}

// A sensitive result reaches only a held parent, and a result that passes
// takes its outside mark with it.
func TestUpPassesWhatTheParentMayRead(t *testing.T) {
	for _, c := range []struct {
		parent, agent Marks
		pass, outside bool
	}{
		{Marks{}, Marks{}, true, false},
		{Marks{}, Marks{Outside: true}, true, true},
		{Marks{Outside: true}, Marks{Outside: true}, true, true},
		{Marks{}, Marks{Sensitive: true}, false, false},
		{Marks{}, Marks{Sensitive: true, Outside: true}, false, false},
		{Marks{Outside: true}, Marks{Sensitive: true}, false, false},
		{Marks{Sensitive: true}, Marks{Sensitive: true}, true, false},
		{Marks{Sensitive: true}, Marks{Sensitive: true, Outside: true}, true, true},
		{Marks{Sensitive: true}, Marks{}, true, false},
	} {
		pass, outside := Up(c.parent, c.agent)
		if pass != c.pass || outside != c.outside {
			t.Errorf("Up(%+v, %+v) = %v, %v; want %v, %v", c.parent, c.agent, pass, outside, c.pass, c.outside)
		}
	}
}
