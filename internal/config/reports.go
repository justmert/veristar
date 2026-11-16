package config

import (
	"slices"
	"strings"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// ReportSigning is the platform's report key (control-plane-api 20.2): the
// stellar key that signs usage reports with sep-53, the g address that key
// must have, and the g addresses of keys it replaced. the address is
// published (the status page, stellar.toml, the docs), so the control
// plane refuses to start with a seed that does not derive it: a report can
// never be signed by a key nobody published.
type ReportSigning struct {
	// Key is the signing keypair; it never prints.
	Key ReportKey
	// Address is REPORT_SIGNING_KEY, the published g address.
	Address string
	// Retired are REPORT_SIGNING_RETIRED_KEYS: earlier report keys whose
	// reports still verify, newest first. optional.
	Retired []string
}

// ReportKey holds the report signing keypair. it never prints.
type ReportKey struct{ kp *keypair.Full }

// Full returns the keypair. call it only at the point of use.
func (k ReportKey) Full() *keypair.Full { return k.kp }

func (k ReportKey) String() string { return redacted }

// GoString keeps the key out of %#v.
func (k ReportKey) GoString() string { return redacted }

// MarshalText keeps the key out of json and text encodings.
func (k ReportKey) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LoadReportSigning reads REPORT_SIGNING_SEED, REPORT_SIGNING_KEY and
// REPORT_SIGNING_RETIRED_KEYS.
func LoadReportSigning(e *Env) (ReportSigning, error) {
	var out ReportSigning
	seed, ok := e.raw("REPORT_SIGNING_SEED")
	switch {
	case !ok:
		e.fail("REPORT_SIGNING_SEED", "is required but not set")
	case !strkey.IsValidEd25519SecretSeed(seed):
		e.fail("REPORT_SIGNING_SEED", "must be a stellar secret seed (S...)")
	default:
		kp, err := keypair.ParseFull(seed)
		if err != nil {
			e.fail("REPORT_SIGNING_SEED", "must be a stellar secret seed (S...)")
		} else {
			out.Key = ReportKey{kp: kp}
		}
	}
	addr, ok := e.raw("REPORT_SIGNING_KEY")
	switch {
	case !ok:
		e.fail("REPORT_SIGNING_KEY", "is required but not set")
	case !strkey.IsValidEd25519PublicKey(addr):
		e.fail("REPORT_SIGNING_KEY", "must be a stellar account id (G...)")
	default:
		out.Address = addr
		if out.Key.kp != nil && out.Key.kp.Address() != addr {
			e.fail("REPORT_SIGNING_KEY", "is not the address of REPORT_SIGNING_SEED; publish the seed's own address")
		}
	}
	if v, ok := e.raw("REPORT_SIGNING_RETIRED_KEYS"); ok {
		for _, part := range strings.Split(v, ",") {
			a := strings.TrimSpace(part)
			if !strkey.IsValidEd25519PublicKey(a) {
				e.fail("REPORT_SIGNING_RETIRED_KEYS", "must be a comma separated list of stellar account ids (G...)")
				break
			}
			if a == addr || slices.Contains(out.Retired, a) {
				e.fail("REPORT_SIGNING_RETIRED_KEYS", "must not repeat a key or name the current one")
				break
			}
			out.Retired = append(out.Retired, a)
		}
	}
	return out, e.Err()
}
