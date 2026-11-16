package chain

import "github.com/justmert/veristar/internal/config"

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
