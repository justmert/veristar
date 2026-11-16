package onchain

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// the two-way link of contract 15.5, built from sep-1 fields only:
//
//   - domain to ledger: the stellar.toml served at the project's verified
//     domain names the account in ACCOUNTS ("a list of stellar accounts
//     that are controlled by this domain"), and its [DOCUMENTATION]
//     ORG_URL is on that same domain;
//   - ledger to domain: the account's home_domain, which only the
//     account's signers can set (SetOptions), is that domain.
//
// both legs together are the link sep-1 itself calls definitive. a
// contract has no home domain; one listed as a [[CURRENCIES]] contract is
// shown as named by the toml.

// LinkAccount is one declared account against one site.
type LinkAccount struct {
	Strkey            string  `json:"strkey"`
	ListedInToml      bool    `json:"listed_in_toml"`
	HomeDomain        *string `json:"home_domain"`
	HomeDomainMatches bool    `json:"home_domain_matches"`
	TwoWay            bool    `json:"two_way"`
}

// LinkContract is one declared contract against one site.
type LinkContract struct {
	Strkey       string `json:"strkey"`
	ListedInToml bool   `json:"listed_in_toml"`
}

// LinkSite is the link of one site's domain.
type LinkSite struct {
	SiteID        uuid.UUID      `json:"site_id"`
	Domain        string         `json:"domain"`
	Verified      bool           `json:"verified"`
	StellarToml   *string        `json:"stellar_toml"`
	OrgURL        *string        `json:"org_url"`
	OrgURLMatches bool           `json:"org_url_matches"`
	Accounts      []LinkAccount  `json:"accounts"`
	Contracts     []LinkContract `json:"contracts"`
	TwoWay        bool           `json:"two_way"`
}

// Link is the project's link: one entry per site that is not removed,
// and two_way when any site holds it.
type Link struct {
	TwoWay bool       `json:"two_way"`
	Sites  []LinkSite `json:"sites"`
}

// tomlSignal is the part of cp.sites.stellar_toml the link reads.
type tomlSignal struct {
	Status           string   `json:"status"`
	OrgURL           *string  `json:"org_url"`
	OrgURLSameDomain *bool    `json:"org_url_same_domain"`
	Accounts         []string `json:"accounts"`
	Currencies       []struct {
		Contract *string `json:"contract"`
	} `json:"currencies"`
}

type siteRow struct {
	ID       uuid.UUID
	Domain   string
	Verified bool
	Toml     []byte
}

func loadLink(ctx context.Context, tx pgx.Tx, sites string, tenant uuid.UUID, ids []idRow) (Link, error) {
	if sites != TenantSites && sites != OperatorSites {
		return Link{}, fmt.Errorf("onchain: unknown sites relation %q", sites)
	}
	rows, err := tx.Query(ctx, `
		select id, domain, verified_at is not null and last_verify_error is null, stellar_toml
		from `+sites+`
		where tenant_id = $1 and status <> 'removed'
		order by created_at, id`, tenant)
	if err != nil {
		return Link{}, fmt.Errorf("onchain: sites: %w", err)
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[siteRow])
	if err != nil {
		return Link{}, fmt.Errorf("onchain: sites: %w", err)
	}
	return buildLink(list, ids)
}

// buildLink computes the link from the sites and the ids' read state.
func buildLink(sites []siteRow, ids []idRow) (Link, error) {
	out := Link{Sites: []LinkSite{}}
	for _, s := range sites {
		ls := LinkSite{SiteID: s.ID, Domain: s.Domain, Verified: s.Verified, Accounts: []LinkAccount{}, Contracts: []LinkContract{}}
		var sig tomlSignal
		if len(s.Toml) > 0 {
			if err := json.Unmarshal(s.Toml, &sig); err != nil {
				return out, fmt.Errorf("onchain: stellar_toml of site %s: %w", s.ID, err)
			}
			ls.StellarToml = &sig.Status
			ls.OrgURL = sig.OrgURL
			ls.OrgURLMatches = sig.Status == "found" && sig.OrgURLSameDomain != nil && *sig.OrgURLSameDomain
		}
		found := sig.Status == "found"
		for _, r := range kinds(ids, "account") {
			a := LinkAccount{Strkey: r.Strkey, ListedInToml: found && slices.Contains(sig.Accounts, r.Strkey)}
			if r.HomeDomain != nil && *r.HomeDomain != "" {
				hd := *r.HomeDomain
				a.HomeDomain = &hd
				a.HomeDomainMatches = strings.EqualFold(strings.TrimSuffix(hd, "."), s.Domain)
			}
			a.TwoWay = s.Verified && ls.OrgURLMatches && a.ListedInToml && a.HomeDomainMatches
			ls.TwoWay = ls.TwoWay || a.TwoWay
			ls.Accounts = append(ls.Accounts, a)
		}
		for _, r := range kinds(ids, "contract") {
			listed := false
			for _, c := range sig.Currencies {
				if found && c.Contract != nil && *c.Contract == r.Strkey {
					listed = true
				}
			}
			ls.Contracts = append(ls.Contracts, LinkContract{Strkey: r.Strkey, ListedInToml: listed})
		}
		out.TwoWay = out.TwoWay || ls.TwoWay
		out.Sites = append(out.Sites, ls)
	}
	return out, nil
}
