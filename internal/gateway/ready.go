package gateway

import (
	"context"
	"errors"

	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/obs"
)

// ReadinessChecks are the /readyz checks: the registry
// snapshot is loaded, today's proof key and daily secrets are available, and
// jetstream acknowledged a publish within CanaryMaxAge, and edge_secrets is
// not known to be missing without a way to recreate it. a node that fails
// any of them would refuse or lose events, so the edge stops routing to it.
func ReadinessChecks(sites Sites, keys *KeyStore, visitor *Visitor, pub *Publisher, buckets *natsx.BucketKeeper) []obs.Check {
	// the queue check is first: it reads only memory, so a nats outage is
	// reported at once instead of after the key reads time out
	return []obs.Check{
		{Name: "queue", Fn: pub.Ready},
		{Name: "buckets", Fn: buckets.Ready},
		{Name: "registry", Fn: func(context.Context) error {
			if !sites.Loaded() {
				return errors.New("gateway: registry snapshot not loaded")
			}
			return nil
		}},
		{Name: "proof_key", Fn: keys.Ready},
		{Name: "daily_secrets", Fn: visitor.Ensure},
	}
}
