package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrUnreadable wraps the error of an entry that does not decode or validate.
var ErrUnreadable = errors.New("registry: unreadable entry")

// GetSite reads one site entry. ok is false when the key does not exist or
// was deleted. the reconciler compares it with the desired entry, so an
// unchanged site is not written again.
func (p *Publisher) GetSite(ctx context.Context, id string) (s Site, ok bool, err error) {
	if err := checkUUID("site id", id); err != nil {
		return Site{}, false, err
	}
	e, err := p.sites.Get(ctx, id)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return Site{}, false, nil
	}
	if err != nil {
		return Site{}, false, fmt.Errorf("registry: get site %s: %w", id, err)
	}
	s, err = DecodeSite(id, e.Value())
	if err != nil {
		// reported as its own kind, so the reconciler overwrites the entry
		// instead of trusting or skipping it
		return Site{}, false, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return s, true, nil
}

// GetOTLPToken reads one registry_otlp entry by its hash. ok is false when
// the key does not exist or was deleted. the reconciler compares it with
// the desired entry, so an unchanged token is not written again.
func (p *Publisher) GetOTLPToken(ctx context.Context, hash string) (t OTLPToken, ok bool, err error) {
	if !hashRe.MatchString(hash) {
		return OTLPToken{}, false, errors.New("registry: otlp token key is not a lowercase hex sha-256")
	}
	e, err := p.otlp.Get(ctx, hash)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return OTLPToken{}, false, nil
	}
	if err != nil {
		return OTLPToken{}, false, fmt.Errorf("registry: get otlp token: %w", err)
	}
	t, err = DecodeOTLPToken(hash, e.Value())
	if err != nil {
		// reported as its own kind, so the reconciler overwrites the entry
		return OTLPToken{}, false, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return t, true, nil
}

// SyncOTLPToken makes the registry entry of a token equal to want and
// reports whether it wrote. an unreadable entry is overwritten.
func (p *Publisher) SyncOTLPToken(ctx context.Context, want OTLPToken) (bool, error) {
	have, ok, err := p.GetOTLPToken(ctx, want.Hash)
	if err != nil && !errors.Is(err, ErrUnreadable) {
		return false, err
	}
	if ok && have == want {
		return false, nil
	}
	if err := p.PutOTLPToken(ctx, want); err != nil {
		return false, err
	}
	return true, nil
}
