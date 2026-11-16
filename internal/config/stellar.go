package config

import (
	"fmt"
	"strconv"
)

// the public mainnet endpoints the on-chain witness reads by default:
// sdf's horizon, and a stellar rpc server from the providers list of
// developers.stellar.org (sdf runs no public mainnet rpc). an operator
// points either key at another provider or at their own node.
const (
	DefaultHorizonURL = "https://horizon.stellar.org"
	DefaultRPCURL     = "https://mainnet.sorobanrpc.com"
	// sdf's horizon allows 3600 requests per hour per address; 0.8 per
	// second leaves headroom for the control plane's own checks, which
	// run from another address in production
	DefaultHorizonRPS = 0.8
	DefaultRPCRPS     = 2
)

// Stellar are the ledger apis of the on-chain witness.
type Stellar struct {
	HorizonURL string
	RPCURL     string
	HorizonRPS float64
	RPCRPS     float64
}

// LoadStellar reads STELLAR_HORIZON_URL, STELLAR_RPC_URL,
// STELLAR_HORIZON_RPS and STELLAR_RPC_RPS. every key is optional and has
// the documented default above; a value that is present is validated.
func LoadStellar(e *Env) (Stellar, error) {
	c := Stellar{HorizonURL: DefaultHorizonURL, RPCURL: DefaultRPCURL, HorizonRPS: DefaultHorizonRPS, RPCRPS: DefaultRPCRPS}
	if e.Has("STELLAR_HORIZON_URL") {
		if u := e.URL("STELLAR_HORIZON_URL"); u != nil {
			c.HorizonURL = u.String()
		}
	}
	if e.Has("STELLAR_RPC_URL") {
		if u := e.URL("STELLAR_RPC_URL"); u != nil {
			c.RPCURL = u.String()
		}
	}
	c.HorizonRPS = e.rate("STELLAR_HORIZON_RPS", c.HorizonRPS)
	c.RPCRPS = e.rate("STELLAR_RPC_RPS", c.RPCRPS)
	return c, e.Err()
}

// rate reads an optional request rate in (0, 100] per second.
func (e *Env) rate(key string, def float64) float64 {
	v, ok := e.raw(key)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f > 100 {
		e.fail(key, fmt.Sprintf("must be a number of requests per second above 0 and at most 100, got %q", v))
		return def
	}
	return f
}
