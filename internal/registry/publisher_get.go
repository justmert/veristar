package registry

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/stellar-analytics/internal/natsx"
)

// NewSitePublisher binds registry_sites only, for a service whose nats user
// may not touch registry_otlp (jobs, which runs the reconciler). its otlp
// methods return an error.
func NewSitePublisher(ctx context.Context, js jetstream.JetStream) (*Publisher, error) {
	sites, err := js.KeyValue(ctx, natsx.BucketRegistrySites)
	if err != nil {
		return nil, fmt.Errorf("registry: bind %s: %w", natsx.BucketRegistrySites, err)
	}
	return &Publisher{sites: sites}, nil
}

// errNoOTLP is returned by the otlp methods of a site-only publisher.
var errNoOTLP = errors.New("registry: this publisher was bound to registry_sites only")

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
