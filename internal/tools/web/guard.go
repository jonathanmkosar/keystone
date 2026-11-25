package web

import (
	"context"
	"net"
	"net/netip"
	"time"
)

// The address checks of web_fetch, for another part that connects to an
// address it did not choose: a remote connector's server (P7-04). The rules
// are the fetcher's own, so there is one list of what is not the public
// internet.

// IsPublic reports whether an address is on the public internet: not this
// machine, not a private or home network, not the shared range, not
// link-local, and not a reserved or special range.
func IsPublic(a netip.Addr) bool { return public(a) }

// IsLocalName reports whether a host name only ever means this machine or
// the local network: localhost, a bare name, or a name under .local, .lan,
// .home, .internal and the like.
func IsLocalName(host string) bool { return localName(host) }

// ErrNotPublic is the error of a dial refused because an address was not
// one the dialler may connect to.
type ErrNotPublic = errNotPublic

// Dial returns a dial function that connects only to addresses allow
// accepts, checked as web_fetch checks them: the name is resolved here,
// every address it resolves to must be allowed, the address dialled is the
// one checked, and the socket checks it again as it connects. Nil allow
// means IsPublic.
func Dial(timeout time.Duration, allow func(netip.Addr) bool) func(ctx context.Context, network, address string) (net.Conn, error) {
	if allow == nil {
		allow = public
	}
	return fetcher{timeout: timeout, allow: allow}.dial
}
