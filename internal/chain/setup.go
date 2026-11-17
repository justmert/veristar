package chain

import (
	"context"

	"github.com/justmert/veristar/internal/config"
)

// UserAgent names the platform to the ledger apis.
const UserAgent = "veristar-onchain-witness"

// Sources builds the horizon and rpc clients of a configuration.
func Sources(c config.Stellar) (*Horizon, *RPC, error) {
	h, err := NewEndpoint(c.HorizonURL, c.HorizonRPS, UserAgent)
	if err != nil {
		return nil, nil, err
	}
	r, err := NewEndpoint(c.RPCURL, c.RPCRPS, UserAgent)
	if err != nil {
		return nil, nil, err
	}
	return NewHorizon(h), NewRPC(r), nil
}

// CheckNetwork compares the network passphrase horizon and rpc announce
// with passphrase. a source on another network would make every proof and
// count speak of other accounts under the same strkeys, so a mismatch is a
// CodeWrongNetwork error. a source that cannot be read gives its read
// error; the caller decides whether that may wait.
func CheckNetwork(ctx context.Context, h *Horizon, r *RPC, passphrase string) error {
	root, err := h.Root(ctx)
	if err != nil {
		return err
	}
	if root.NetworkPassphrase != passphrase {
		return fail(CodeWrongNetwork, "horizon %s serves %q, not %q", h.Endpoint().URL(), root.NetworkPassphrase, passphrase)
	}
	got, err := r.Network(ctx)
	if err != nil {
		return err
	}
	if got != passphrase {
		return fail(CodeWrongNetwork, "rpc %s serves %q, not %q", r.Endpoint().URL(), got, passphrase)
	}
	return nil
}
