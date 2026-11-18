package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"regexp"
	"strconv"

	"github.com/google/uuid"
)

// WebEvent is what the gateway publishes to WEB_EVENTS and the replayer sends
// to umami. it never carries an ip address or the proof.
type WebEvent struct {
	// ID is a uuidv7 and also the Nats-Msg-Id.
	ID string `json:"id"`
	// SiteID is the registered site, also the subject token.
	SiteID string `json:"site_id"`
	// WebsiteID is the umami website the band selects: the site id for
	// primary, the site's quarantine website for quarantine.
	WebsiteID string `json:"website_id"`
	Band      string `json:"band"`
	// VT is the visitor token, lowercase hex hmac-sha256.
	VT string `json:"vt"`
	// UA is the original user agent, cut to MaxUserAgent characters.
	UA string `json:"ua"`
	// Country is an iso 3166-1 alpha-2 code or empty.
	Country  string `json:"country"`
	Type     string `json:"type"`
	Hostname string `json:"hostname"`
	// URL is the path plus the allowlisted query.
	URL      string         `json:"url"`
	Referrer string         `json:"referrer"`
	Title    string         `json:"title"`
	Screen   string         `json:"screen"`
	Language string         `json:"language"`
	Name     string         `json:"name,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
	// AcceptedAt is the gateway acceptance time in unix milliseconds.
	AcceptedAt int64 `json:"accepted_at"`
}

// NewEventID returns a new uuidv7. time-ordered ids keep the duplicate guard
// keys in visit_state roughly in arrival order.
func NewEventID() (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

var (
	vtRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Partitions is the fixed number of WEB_EVENTS partitions. every visitor's
// events go to one partition, and each partition has one consumer and one
// owner at a time, so the replayers of several nodes share the stream without
// two of them handling one visitor. 32 splits the three core nodes 11, 11
// and 10 (the most loaded node takes 34 % instead of an even 33 %), divides
// evenly over 1, 2, 4 and 8 nodes, and keeps the cost per partition small:
// each one is a replicated consumer and a lock renewed every 5 s. the count
// is part of every subject, so changing it is a migration of its own.
const Partitions = 32

// Partition returns the partition of a visitor, 0 to Partitions-1. it is
// fnv-1a over a domain prefix, the site id and the visitor token, stable
// across processes, releases and restarts. the low bits of fnv-1a depend
// only on the low bits of the input bytes, so the sum goes through the
// murmur3 finalizer before the modulo. the prefix keeps it independent of
// the replayer's umami process hash over the same two values, so the
// partitions a node owns still spread its sends over every umami process.
func Partition(siteID, vt string) int {
	f := fnv.New64a()
	_, _ = f.Write([]byte("partition\x00"))
	_, _ = f.Write([]byte(siteID))
	_, _ = f.Write([]byte{0})
	_, _ = f.Write([]byte(vt))
	h := f.Sum64()
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return int(h % Partitions)
}

// PartitionToken is the subject token of a partition: two digits, so the
// subjects sort in partition order.
func PartitionToken(p int) string {
	if p < 10 {
		return "0" + strconv.Itoa(p)
	}
	return strconv.Itoa(p)
}

// Subject is the WEB_EVENTS subject of the event:
// web.events.<site id>.<partition>.
func (e *WebEvent) Subject() string {
	return "web.events." + e.SiteID + "." + PartitionToken(Partition(e.SiteID, e.VT))
}

// LegacySubject is the subject releases before the partitions published to,
// web.events.<site id>. the replayer still drains it during an upgrade.
func (e *WebEvent) LegacySubject() string { return "web.events." + e.SiteID }

func canonicalUUID(s string) (uuid.UUID, bool) {
	u, err := uuid.Parse(s)
	return u, err == nil && u.String() == s
}

// Validate checks every field against the field limits.
func (e *WebEvent) Validate() error {
	id, ok := canonicalUUID(e.ID)
	if !ok || id.Version() != 7 {
		return errors.New("events: id is not a canonical uuidv7")
	}
	if _, ok := canonicalUUID(e.SiteID); !ok {
		return errors.New("events: site_id is not a canonical uuid")
	}
	if _, ok := canonicalUUID(e.WebsiteID); !ok {
		return errors.New("events: website_id is not a canonical uuid")
	}
	switch e.Band {
	case BandPrimary:
		if e.WebsiteID != e.SiteID {
			return errors.New("events: a primary event must go to the website that equals the site id")
		}
	case BandQuarantine:
		if e.WebsiteID == e.SiteID {
			return errors.New("events: a quarantine event must go to the quarantine website")
		}
	default:
		return fmt.Errorf("events: band %q is not primary or quarantine", e.Band)
	}
	if !vtRe.MatchString(e.VT) {
		return errors.New("events: vt is not 64 lowercase hex characters")
	}
	if tooLong(e.UA, MaxUserAgent) || hasControl(e.UA) {
		return errors.New("events: ua is too long or has control characters")
	}
	if e.Country != "" && !countryRe.MatchString(e.Country) {
		return errors.New("events: country is not an iso 3166-1 alpha-2 code")
	}
	if e.AcceptedAt <= 0 {
		return errors.New("events: accepted_at is not set")
	}
	checks := []struct {
		name       string
		v          string
		max        int
		identifier bool
	}{
		{"hostname", e.Hostname, MaxHostname, true},
		{"url", e.URL, MaxURL, true},
		{"referrer", e.Referrer, MaxReferrer, false},
		{"title", e.Title, MaxTitle, false},
		{"screen", e.Screen, MaxScreen, true},
		{"language", e.Language, MaxLanguage, true},
		{"name", e.Name, MaxEventName, true},
	}
	for _, c := range checks {
		if tooLong(c.v, c.max) || (c.identifier && hasControl(c.v)) || hasNUL(c.v) {
			return fmt.Errorf("events: %s is over %d characters or has control characters", c.name, c.max)
		}
	}
	if e.Hostname == "" || e.URL == "" || e.URL[0] != '/' {
		return errors.New("events: hostname and a url starting with / are required")
	}
	switch e.Type {
	case TypePageview:
		if e.Name != "" || len(e.Data) > 0 {
			return errors.New("events: a pageview carries no event name or data")
		}
	case TypeEvent:
		if e.Name == "" {
			return errors.New("events: a custom event needs a name")
		}
		if err := validateData(e.Data); err != nil {
			return err
		}
	default:
		return fmt.Errorf("events: type %q is not pv or ev", e.Type)
	}
	return nil
}

func validateData(d map[string]any) error {
	if len(d) > MaxDataKeys {
		return fmt.Errorf("events: event data has more than %d keys", MaxDataKeys)
	}
	for k, v := range d {
		if k == "" || tooLong(k, MaxDataKey) || hasControl(k) {
			return errors.New("events: invalid event data key")
		}
		switch x := v.(type) {
		case string:
			if tooLong(x, MaxDataValue) || hasNUL(x) {
				return errors.New("events: event data value too long or has a nul")
			}
		case json.Number:
			if len(x) > maxNumberLiteral {
				return errors.New("events: event data number too long")
			}
		case bool:
		default:
			return fmt.Errorf("events: event data value of type %T is not a scalar", v)
		}
	}
	return nil
}

// Marshal validates and encodes the event.
func (e *WebEvent) Marshal() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

// DecodeWebEvent decodes and validates a WEB_EVENTS message. numbers in
// event data stay json.Number, so they reach umami exactly as sent.
func DecodeWebEvent(b []byte) (*WebEvent, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var e WebEvent
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("events: decode web event: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("events: trailing data after web event")
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}
