package models

// OnHomeNetwork reports whether a host is this machine or one on the home
// network, read from its name or address alone, by the rule a local entry's
// endpoint is checked against (decision 0051). Nothing is looked up. A
// SearXNG search provider's address is checked by the same rule (P7-07).
func OnHomeNetwork(host string) bool { return onHomeNetwork(host) }
