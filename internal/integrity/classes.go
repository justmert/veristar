// Package integrity holds the evidence classes the collect gateway assigns
// to each event's network and the versioned rule set that turns a site's
// daily evidence into an integrity grade with reasons. the gateway, the
// jobs and the control plane share it, so the names on EDGE_STATS, in the
// database and in the api are one set.
package integrity

import "regexp"

// NetClass is the class of the network an event arrived from. the gateway
// derives it in memory from the client address; only the class leaves the
// request.
type NetClass string

// the network classes. access covers residential broadband and mobile
// carriers, organisation covers business, education and government
// networks, hosting covers cloud, hosting and content networks (where most
// vpn and proxy exits live), anonymiser covers published tor exit
// addresses, and unknown is every address the open data cannot place.
const (
	NetAccess       NetClass = "access"
	NetOrganisation NetClass = "organisation"
	NetHosting      NetClass = "hosting"
	NetAnonymiser   NetClass = "anonymiser"
	NetUnknown      NetClass = "unknown"
)

// NetClasses lists every class in display order.
var NetClasses = []NetClass{NetAccess, NetOrganisation, NetHosting, NetAnonymiser, NetUnknown}

// Valid reports whether c is one of NetClasses.
func (c NetClass) Valid() bool {
	switch c {
	case NetAccess, NetOrganisation, NetHosting, NetAnonymiser, NetUnknown:
		return true
	}
	return false
}

// Discounted reports whether events from the class count against a site's
// grade: hosting networks and anonymisers carry almost no ordinary
// visitors, so a large share of them is evidence of automation.
func (c NetClass) Discounted() bool { return c == NetHosting || c == NetAnonymiser }

// EDGE_STATS outcome families of the evidence counters. an evidence outcome
// counts accepted and quarantined events by network class; a network
// outcome records that a site saw a network on this utc day, keyed by an
// hmac of the autonomous system number under the gateways' daily secret.
const (
	EvidencePrefix = "evidence_net_"
	NetworkPrefix  = "network_"
	// NetworkKeyHexLen is the length of the hex network key on EDGE_STATS:
	// 64 bits, enough to keep the distinct count exact for the few thousand
	// networks one site sees in a day.
	NetworkKeyHexLen = 16
)

var networkOutcomeRe = regexp.MustCompile(`^network_[0-9a-f]{16}$`)

// EvidenceOutcome is the EDGE_STATS outcome that counts one event of class c.
func EvidenceOutcome(c NetClass) string { return EvidencePrefix + string(c) }

// ParseEvidenceOutcome returns the class of an evidence outcome.
func ParseEvidenceOutcome(outcome string) (NetClass, bool) {
	if len(outcome) <= len(EvidencePrefix) || outcome[:len(EvidencePrefix)] != EvidencePrefix {
		return "", false
	}
	c := NetClass(outcome[len(EvidencePrefix):])
	return c, c.Valid()
}

// IsNetworkOutcome reports whether outcome is a network sighting.
func IsNetworkOutcome(outcome string) bool { return networkOutcomeRe.MatchString(outcome) }
