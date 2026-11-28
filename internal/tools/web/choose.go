package web

// Choosing the order a search tries its candidates in. The candidates are
// the person's own ready providers in their order, then the install's ready
// providers in the admin's order, and web.strategy orders each group: a
// person's own always come first (decision 0682).

// candidate is one provider a search may try.
type candidate struct {
	place   place
	p       Provider
	kind    Kind
	adapter Adapter
	key     string
	print   string
	st      State
	// trial says this search is the provider's soft test: it is out or
	// failing, its test is due, and it has no free check.
	trial bool
}

// order puts one group's candidates in the order web.strategy says. pick
// returns a number from 0 up to 1; the weighted strategy draws with it.
func (r Rotation) order(group []candidate, pick func() float64) []candidate {
	switch r.Strategy {
	case StrategyOrder:
		return group
	case StrategyWeighted:
		return r.weighted(group, pick)
	default: // free_first
		var free, rest []candidate
		for _, c := range group {
			if freeLeft(c.p, c.st) {
				free = append(free, c)
			} else {
				rest = append(rest, c)
			}
		}
		return append(free, rest...)
	}
}

// weight is a candidate's share of its quota left, from 0 to 1. A quota
// that is not known counts as web.unknown_weight.
func (r Rotation) weight(c candidate) float64 {
	left, from := leftOf(c.p, c.st)
	if from == "" || c.p.MonthlyQuota <= 0 {
		return r.UnknownWeight
	}
	return min(1, float64(left)/float64(c.p.MonthlyQuota))
}

// weighted spreads searches across the candidates by their share of quota
// left: each place is drawn in turn, with a chance in proportion to each
// remaining candidate's weight. A provider with no quota at all, such as a
// SearXNG on the owner's machine, comes after all the others.
func (r Rotation) weighted(group []candidate, pick func() float64) []candidate {
	var metered, unmetered []candidate
	for _, c := range group {
		if c.kind.Unmetered {
			unmetered = append(unmetered, c)
		} else {
			metered = append(metered, c)
		}
	}
	out := make([]candidate, 0, len(group))
	for len(metered) > 0 {
		total := 0.0
		for _, c := range metered {
			total += r.weight(c)
		}
		i := 0
		if total > 0 {
			at := pick() * total
			for i = 0; i < len(metered)-1; i++ {
				at -= r.weight(metered[i])
				if at < 0 {
					break
				}
			}
		}
		out = append(out, metered[i])
		metered = append(metered[:i:i], metered[i+1:]...)
	}
	return append(out, unmetered...)
}
