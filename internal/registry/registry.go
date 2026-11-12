// Package registry publishes and watches the site and otlp token registries
// in nats kv. the control plane writes them, edge services read them from an
// in-memory snapshot, so the edge keeps working when the control plane, the
// database or nats itself is unavailable.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/natsx"
)

// StatusActive is the only status that lets traffic through. every other
// status (pending, suspended, removed, revoked, ...) is refused, so a new
// status added later fails closed.
const StatusActive = "active"

// Site is one entry of registry_sites, keyed by site id.
type Site struct {
	ID                  string   `json:"-"`
	TenantID            string   `json:"tenant_id"`
	Status              string   `json:"status"`
	Origins             []string `json:"origins"`
	QuarantineWebsiteID string   `json:"quarantine_website_id"`
}

// Active reports whether the gateway accepts events for the site.
func (s Site) Active() bool { return s.Status == StatusActive }

// OTLPToken is one entry of registry_otlp, keyed by the sha-256 of the token.
type OTLPToken struct {
	Hash     string `json:"-"`
	TenantID string `json:"tenant_id"`
	TokenID  string `json:"token_id"`
	Status   string `json:"status"`
}

// Active reports whether the collector accepts the token.
func (t OTLPToken) Active() bool { return t.Status == StatusActive }

// HashOTLPToken returns the registry key of a token: lowercase hex sha-256.
// the token itself is never stored.
func HashOTLPToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

var (
	statusRe = regexp.MustCompile(`^[a-z_]{1,32}$`)
	hashRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func checkUUID(field, v string) error {
	u, err := uuid.Parse(v)
	if err != nil || u.String() != v {
		return fmt.Errorf("registry: %s %q is not a canonical uuid", field, v)
	}
	return nil
}

// Validate checks a site before it is published or after it is read.
func (s Site) Validate() error {
	if err := checkUUID("site id", s.ID); err != nil {
		return err
	}
	if err := checkUUID("tenant id", s.TenantID); err != nil {
		return err
	}
	if err := checkUUID("quarantine website id", s.QuarantineWebsiteID); err != nil {
		return err
	}
	if !statusRe.MatchString(s.Status) {
		return fmt.Errorf("registry: site %s has invalid status %q", s.ID, s.Status)
	}
	if s.Active() && len(s.Origins) == 0 {
		return fmt.Errorf("registry: active site %s has no origins", s.ID)
	}
	for _, o := range s.Origins {
		if o == "" {
			return fmt.Errorf("registry: site %s has an empty origin", s.ID)
		}
	}
	return nil
}

// Validate checks a token entry.
func (t OTLPToken) Validate() error {
	if !hashRe.MatchString(t.Hash) {
		return errors.New("registry: otlp token key is not a lowercase hex sha-256")
	}
	if err := checkUUID("tenant id", t.TenantID); err != nil {
		return err
	}
	if err := checkUUID("token id", t.TokenID); err != nil {
		return err
	}
	if !statusRe.MatchString(t.Status) {
		return fmt.Errorf("registry: token %s has invalid status %q", t.TokenID, t.Status)
	}
	return nil
}

// DecodeSite parses a registry_sites value.
func DecodeSite(key string, value []byte) (Site, error) {
	var s Site
	if err := json.Unmarshal(value, &s); err != nil {
		return Site{}, fmt.Errorf("registry: site %s: %w", key, err)
	}
	s.ID = key
	return s, s.Validate()
}

// DecodeOTLPToken parses a registry_otlp value.
func DecodeOTLPToken(key string, value []byte) (OTLPToken, error) {
	var t OTLPToken
	if err := json.Unmarshal(value, &t); err != nil {
		return OTLPToken{}, fmt.Errorf("registry: otlp token: %w", err)
	}
	t.Hash = key
	return t, t.Validate()
}

// Publisher writes the registries. the control plane is its only user.
type Publisher struct {
	sites jetstream.KeyValue
	otlp  jetstream.KeyValue
}

// NewPublisher binds both registry buckets.
func NewPublisher(ctx context.Context, js jetstream.JetStream) (*Publisher, error) {
	sites, err := js.KeyValue(ctx, natsx.BucketRegistrySites)
	if err != nil {
		return nil, fmt.Errorf("registry: bind %s: %w", natsx.BucketRegistrySites, err)
	}
	otlp, err := js.KeyValue(ctx, natsx.BucketRegistryOTLP)
	if err != nil {
		return nil, fmt.Errorf("registry: bind %s: %w", natsx.BucketRegistryOTLP, err)
	}
	return &Publisher{sites: sites, otlp: otlp}, nil
}

// PutSite stores or replaces a site.
func (p *Publisher) PutSite(ctx context.Context, s Site) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := p.sites.Put(ctx, s.ID, b); err != nil {
		return fmt.Errorf("registry: put site %s: %w", s.ID, err)
	}
	return nil
}

// DeleteSite removes a site; watchers drop it from their snapshots.
func (p *Publisher) DeleteSite(ctx context.Context, id string) error {
	if err := checkUUID("site id", id); err != nil {
		return err
	}
	if err := p.sites.Delete(ctx, id); err != nil {
		return fmt.Errorf("registry: delete site %s: %w", id, err)
	}
	return nil
}

// PutOTLPToken stores or replaces a token entry.
func (p *Publisher) PutOTLPToken(ctx context.Context, t OTLPToken) error {
	if err := t.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if _, err := p.otlp.Put(ctx, t.Hash, b); err != nil {
		return fmt.Errorf("registry: put otlp token %s: %w", t.TokenID, err)
	}
	return nil
}

// DeleteOTLPToken removes a token entry by its hash.
func (p *Publisher) DeleteOTLPToken(ctx context.Context, hash string) error {
	if !hashRe.MatchString(hash) {
		return errors.New("registry: otlp token key is not a lowercase hex sha-256")
	}
	if err := p.otlp.Delete(ctx, hash); err != nil {
		return fmt.Errorf("registry: delete otlp token: %w", err)
	}
	return nil
}
