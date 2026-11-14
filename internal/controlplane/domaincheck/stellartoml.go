package domaincheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"golang.org/x/net/idna"

	"github.com/justmert/veristar/internal/netguard"
)

// the sep-1 location and size cap. they equal the constants of the sdk's
// clients/stellartoml package (a test asserts it); that package is not
// imported because it drags a second toml library and testify/mock into
// the binary.
const (
	StellarTomlPath    = "/.well-known/stellar.toml"
	StellarTomlMaxSize = 100 * 1024
)

// statuses of StellarToml (control-plane-api 5.12)
const (
	TomlFound    = "found"
	TomlNotFound = "not_found"
	TomlInvalid  = "invalid"
	TomlTooLarge = "too_large"
	TomlRefused  = "refused"
	TomlError    = "error"
)

// parser error codes
const (
	CodeTomlSyntax = "toml_syntax"
	CodeTomlType   = "toml_type"
)

// caps on what is kept from the file, so tenant-controlled text stays short
const (
	maxCurrencies   = 50
	maxCodeRunes    = 12
	maxStrkeyRunes  = 56
	maxVersionRunes = 32
	maxPassRunes    = 256
	maxURLRunes     = 2048
)

// Currency is one [[CURRENCIES]] entry.
type Currency struct {
	Code     string  `json:"code"`
	Issuer   *string `json:"issuer"`
	Contract *string `json:"contract"`
	Valid    bool    `json:"valid"`
}

// StellarToml is the signal stored in cp.sites.stellar_toml.
type StellarToml struct {
	Status            string     `json:"status"`
	Error             *string    `json:"error"`
	HTTPStatus        *int       `json:"http_status"`
	Bytes             *int       `json:"bytes"`
	Version           *string    `json:"version"`
	NetworkPassphrase *string    `json:"network_passphrase"`
	Network           *string    `json:"network"`
	AccountsCount     int        `json:"accounts_count"`
	AccountsValid     int        `json:"accounts_valid"`
	SigningKeyPresent bool       `json:"signing_key_present"`
	SigningKeyValid   bool       `json:"signing_key_valid"`
	OrgURL            *string    `json:"org_url"`
	OrgURLSameDomain  *bool      `json:"org_url_same_domain"`
	Currencies        []Currency `json:"currencies"`
	CurrenciesTotal   int        `json:"currencies_total"`
}

func ptr[T any](v T) *T { return &v }

func empty(status string, code *string, httpStatus *int) StellarToml {
	return StellarToml{Status: status, Error: code, HTTPStatus: httpStatus, Currencies: []Currency{}}
}

// tomlFile holds the fields the signal reads. ORG_URL lives in the
// [DOCUMENTATION] table per sep-1, not at the top level.
type tomlFile struct {
	Version           *string  `toml:"VERSION"`
	NetworkPassphrase *string  `toml:"NETWORK_PASSPHRASE"`
	Accounts          []string `toml:"ACCOUNTS"`
	SigningKey        *string  `toml:"SIGNING_KEY"`
	Documentation     struct {
		OrgURL *string `toml:"ORG_URL"`
	} `toml:"DOCUMENTATION"`
	Currencies []struct {
		Code     *string `toml:"code"`
		Issuer   *string `toml:"issuer"`
		Contract *string `toml:"contract"`
	} `toml:"CURRENCIES"`
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func cutPtr(s *string, n int) *string {
	if s == nil {
		return nil
	}
	return ptr(cut(*s, n))
}

// networkName maps a passphrase to the sdk's named networks.
func networkName(p string) string {
	switch p {
	case network.PublicNetworkPassphrase:
		return "public"
	case network.TestNetworkPassphrase:
		return "testnet"
	case network.FutureNetworkPassphrase:
		return "futurenet"
	}
	return "other"
}

// ParseStellarToml reads a stellar.toml body fetched for domain. a file
// that is not toml is toml_syntax; a read field of the wrong type is
// toml_type. both are status invalid.
func ParseStellarToml(domain string, body []byte) StellarToml {
	ok := http.StatusOK
	// a generic decode first separates syntax errors from type errors
	var raw map[string]any
	if err := toml.Unmarshal(body, &raw); err != nil {
		return empty(TomlInvalid, ptr(CodeTomlSyntax), &ok)
	}
	var f tomlFile
	if err := toml.Unmarshal(body, &f); err != nil {
		return empty(TomlInvalid, ptr(CodeTomlType), &ok)
	}
	out := empty(TomlFound, nil, &ok)
	out.Bytes = ptr(len(body))
	out.Version = cutPtr(f.Version, maxVersionRunes)
	if f.NetworkPassphrase != nil {
		out.NetworkPassphrase = cutPtr(f.NetworkPassphrase, maxPassRunes)
		out.Network = ptr(networkName(*f.NetworkPassphrase))
	}
	out.AccountsCount = len(f.Accounts)
	for _, a := range f.Accounts {
		if strkey.IsValidEd25519PublicKey(a) {
			out.AccountsValid++
		}
	}
	if f.SigningKey != nil {
		out.SigningKeyPresent = true
		out.SigningKeyValid = strkey.IsValidEd25519PublicKey(*f.SigningKey)
	}
	if u := f.Documentation.OrgURL; u != nil {
		out.OrgURL = cutPtr(u, maxURLRunes)
		out.OrgURLSameDomain = ptr(sameDomain(*u, domain))
	}
	out.CurrenciesTotal = len(f.Currencies)
	for i, c := range f.Currencies {
		if i == maxCurrencies {
			break
		}
		cur := Currency{Issuer: cutPtr(c.Issuer, maxStrkeyRunes), Contract: cutPtr(c.Contract, maxStrkeyRunes)}
		if c.Code != nil {
			cur.Code = cut(*c.Code, maxCodeRunes)
		}
		// sep-1: a classic asset has an issuer, a sep-41 token a contract,
		// never both
		switch {
		case c.Issuer != nil && c.Contract == nil:
			cur.Valid = strkey.IsValidEd25519PublicKey(*c.Issuer)
		case c.Contract != nil && c.Issuer == nil:
			cur.Valid = strkey.IsValidContractAddress(*c.Contract)
		}
		out.Currencies = append(out.Currencies, cur)
	}
	return out
}

// sameDomain reports whether an https url's host is domain.
func sameDomain(raw, domain string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return false
	}
	h, err := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	return err == nil && h == domain
}

// FetchStellarToml fetches and parses https://<domain>/.well-known/stellar.toml
// through the guard.
func FetchStellarToml(ctx context.Context, g *netguard.Guard, domain string) StellarToml {
	r, err := g.Get(ctx, domain, StellarTomlPath, StellarTomlMaxSize)
	if err != nil {
		code := netguard.Code(err)
		if code == netguard.CodeRefusedAddress || code == netguard.CodeRedirectRefused {
			return empty(TomlRefused, &code, nil)
		}
		return empty(TomlError, &code, nil)
	}
	if r.Status != http.StatusOK {
		return empty(TomlNotFound, nil, ptr(r.Status))
	}
	if r.TooLarge {
		return empty(TomlTooLarge, nil, ptr(r.Status))
	}
	return ParseStellarToml(domain, r.Body)
}

// tomlChange compares a stored signal with a new one. the audit row is
// written only when status or network changed (control-plane-api 5.12).
func tomlChange(prev []byte, next StellarToml) (map[string]any, bool, error) {
	var old struct {
		Status  *string `json:"status"`
		Network *string `json:"network"`
	}
	if len(prev) > 0 {
		if err := json.Unmarshal(prev, &old); err != nil {
			return nil, false, errors.New("domaincheck: stored stellar_toml is not an object")
		}
	}
	if old.Status != nil && *old.Status == next.Status && eq(old.Network, next.Network) {
		return nil, false, nil
	}
	return map[string]any{
		"status":  map[string]any{"old": old.Status, "new": next.Status},
		"network": map[string]any{"old": old.Network, "new": next.Network},
	}, true, nil
}

func eq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
