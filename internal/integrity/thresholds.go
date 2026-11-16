package integrity

// MaxPrefixThreshold bounds the threshold of any prefix. the gateway keeps
// up to threshold visitor tokens per site and prefix in memory, so the cap
// bounds memory and gossip, and no single /24 or /48 legitimately carries
// more distinct visitors of one site in an hour.
const MaxPrefixThreshold = 2000

// accessShareDivisor and hostingShareDivisor set how much of a site's
// typical daily visitors one prefix may carry in an hour: one percent for
// access, organisation and unknown networks (a carrier-grade nat prefix of
// a large mobile carrier routinely carries that much of a popular site), one
// per mille for hosting networks and anonymisers, which carry almost no
// ordinary visitors.
const (
	accessShareDivisor  = 100
	hostingShareDivisor = 1000
	// hostingFloorDivisor makes the hosting floor a quarter of the
	// configured floor: with IPCLUSTER_THRESHOLD 20, five distinct visitors
	// behind one hosting prefix in an hour already quarantine it.
	hostingFloorDivisor = 4
)

// PrefixThreshold is how many distinct visitor tokens one truncated prefix
// (/24 or /48) may show for a site within an hour before the prefix's events
// go to the quarantine band. floor is IPCLUSTER_THRESHOLD; scale is the
// site's typical daily visitors (registry.Site.Scale, 0 for a new site).
//
// the configured floor holds for every access, organisation and unknown
// network and grows with the site's own traffic, so popular and mobile-first
// sites are not quarantined for carrier-grade nat. hosting and anonymiser
// prefixes get a quarter of the floor and grow ten times slower.
func PrefixThreshold(floor int, class NetClass, scale int) int {
	floor = max(floor, 1)
	scale = max(scale, 0)
	var t int
	if class.Discounted() {
		t = max((floor+hostingFloorDivisor-1)/hostingFloorDivisor, scale/hostingShareDivisor)
	} else {
		t = max(floor, scale/accessShareDivisor)
	}
	return min(max(t, 1), MaxPrefixThreshold)
}

// ScaleOf rounds a site's typical daily visitors down to a power of two, so
// the value in the site registry changes only when the site's traffic
// doubles or halves. zero and negative inputs give zero.
func ScaleOf(typicalDailyVisitors int64) int {
	if typicalDailyVisitors < 1 {
		return 0
	}
	s := int64(1)
	for s*2 <= typicalDailyVisitors && s < 1<<30 {
		s *= 2
	}
	return int(s)
}
