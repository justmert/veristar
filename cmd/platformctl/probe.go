package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/operator"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/controlplane/sites"
	pdb "github.com/justmert/veristar/internal/db"
)

const probeUsage = `  probe-tenant create -operator-github-id N -domain D -origin URL
                    create the internal monitoring tenant:
                    an active tenant with is_internal set, one active site,
                    one otlp token and one machine api key scoped to it.
                    prints the site id, token and key once; the jobs
                    reconcile task provisions umami and the registries.
  probe-tenant rotate-otlp-token
                    give the internal tenant a new otlp token and revoke
                    every active one it had, in one transaction. prints the
                    new token once; the jobs reconcile task writes both
                    states to the token registry
`

// probeSlug is the probe tenant's slug. there is one probe tenant per
// environment, so a second create is refused instead of making another.
const probeSlug = "internal-probe"

// probeActor marks the audit rows of this command; the operator named by
// -operator-github-id is recorded as created_by, as the schema requires
const probeActor = "system:platformctl-probe-tenant"

// ProbeTenant is what probe-tenant create made.
type ProbeTenant struct {
	TenantID  uuid.UUID
	SiteID    uuid.UUID
	OTLPToken string
	APIKey    string
}

func runProbeTenant(ctx context.Context, args []string, env *config.Env, stdout io.Writer) error {
	if slices.Equal(args, []string{"rotate-otlp-token"}) {
		poolCfg, err := pdb.LoadPoolConfig(env, pdb.RoleCPSystem)
		if err != nil {
			return err
		}
		d, err := pdb.Open(ctx, poolCfg, "platformctl")
		if err != nil {
			return err
		}
		defer d.Close()
		tok, revoked, err := rotateProbeOTLPToken(ctx, d)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "otlp_token=%s\nrevoked=%d\n", tok, revoked)
		return err
	}
	if len(args) < 1 || args[0] != "create" {
		return errors.New("usage: platformctl probe-tenant create -operator-github-id N -domain D -origin URL | rotate-otlp-token")
	}
	fs := flag.NewFlagSet("probe-tenant create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	ghID := fs.Int64("operator-github-id", 0, "github id of an operator who signed in once")
	domain := fs.String("domain", "", "the probe site's domain, e.g. probe.<BASE_DOMAIN>")
	origin := fs.String("origin", "", "the origin the prober sends, e.g. https://probe.<BASE_DOMAIN>")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *ghID <= 0 || !sites.ValidDomain(*domain) {
		return errors.New("probe-tenant create needs -operator-github-id and a valid -domain")
	}
	o, err := url.Parse(*origin)
	if err != nil || (o.Scheme != "https" && o.Scheme != "http") || o.Hostname() != *domain || o.Path != "" {
		return errors.New("-origin must be http(s)://<domain> with the same host as -domain")
	}
	poolCfg, err := pdb.LoadPoolConfig(env, pdb.RoleCPSystem)
	if err != nil {
		return err
	}
	d, err := pdb.Open(ctx, poolCfg, "platformctl")
	if err != nil {
		return err
	}
	defer d.Close()
	pt, err := createProbeTenant(ctx, d, *ghID, *domain, *origin)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "tenant_id=%s\nsite_id=%s\notlp_token=%s\nmachine_api_key=%s\n", pt.TenantID, pt.SiteID, pt.OTLPToken, pt.APIKey)
	return err
}

func createProbeTenant(ctx context.Context, d *pdb.DB, ghID int64, domain, origin string) (ProbeTenant, error) {
	tok, err := otlptokens.Generate()
	if err != nil {
		return ProbeTenant{}, err
	}
	key, err := operator.GenerateKey()
	if err != nil {
		return ProbeTenant{}, err
	}
	siteID, err := uuid.NewV7()
	if err != nil {
		return ProbeTenant{}, err
	}
	qid, err := uuid.NewV7()
	if err != nil {
		return ProbeTenant{}, err
	}
	vb := make([]byte, 32)
	if _, err := rand.Read(vb); err != nil {
		return ProbeTenant{}, err
	}
	out := ProbeTenant{SiteID: siteID, OTLPToken: tok.Plain, APIKey: key.Plain}
	err = d.WithSystem(ctx, func(tx pgx.Tx) error {
		var opID uuid.UUID
		err := tx.QueryRow(ctx, `select id from cp.users where github_id = $1 and is_operator`, ghID).Scan(&opID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no operator with github id %d has signed in; sign in to the console once first", ghID)
		}
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from cp.tenants where slug = $1 or is_internal)`, probeSlug).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errors.New("an internal tenant already exists; the probe tenant is created once per environment")
		}
		if err := tx.QueryRow(ctx, `insert into cp.tenants (slug, name, description, status, is_internal, created_by, approved_by)
			values ($1, 'Internal probe', 'monitoring probe tenant, hidden from operator views', 'active', true, $2, $2)
			returning id`, probeSlug, opID).Scan(&out.TenantID); err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}
		if _, err := tx.Exec(ctx, `insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
			values ($1, $2, 'Probe', $3, $4, $5, 'active', $6)`,
			siteID, out.TenantID, domain, []string{origin}, qid, base64.RawURLEncoding.EncodeToString(vb)); err != nil {
			return fmt.Errorf("insert site: %w", err)
		}
		var tokID uuid.UUID
		if err := tx.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, environment, prefix, hash, created_by)
			values ($1, 'probe', '', $2, $3, $4) returning id`, out.TenantID, tok.Display, tok.Hash[:], opID).Scan(&tokID); err != nil {
			return fmt.Errorf("insert otlp token: %w", err)
		}
		var keyID uuid.UUID
		if err := tx.QueryRow(ctx, `insert into cp.operator_api_keys (name, prefix, hash, scope, scope_tenant_id, expires_at, created_by)
			values ('probe', $1, $2, 'tenant', $3, now() + make_interval(days => $4), $5) returning id`,
			key.Display, key.Hash[:], out.TenantID, operator.MaxKeyDays, opID).Scan(&keyID); err != nil {
			return fmt.Errorf("insert api key: %w", err)
		}
		for _, e := range []audit.Entry{
			{Actor: probeActor, Action: audit.TenantCreate, Target: audit.TenantTarget(out.TenantID),
				Detail: map[string]any{"slug": probeSlug, "is_internal": true, "created_by": opID}},
			{Actor: probeActor, Action: audit.SiteCreate, Target: audit.SiteTarget(siteID),
				Detail: map[string]any{"tenant_id": out.TenantID, "domain": domain, "origins": []string{origin}, "status": "active", "quarantine_website_id": qid}},
			{Actor: probeActor, Action: audit.OTLPTokenCreate, Target: audit.OTLPTokenTarget(tokID),
				Detail: map[string]any{"tenant_id": out.TenantID, "prefix": tok.Display}},
			{Actor: probeActor, Action: operator.ActionAPIKeyCreate, Target: operator.APIKeyTarget(keyID),
				Detail: map[string]any{"tenant_id": out.TenantID.String(), "prefix": key.Display, "scope": operator.ScopeMetricsRead}},
		} {
			if err := audit.InSystem(ctx, tx, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ProbeTenant{}, err
	}
	return out, nil
}

// rotateProbeOTLPToken adds a new otlp token to the internal tenant and
// revokes its active ones in the same transaction, so the tenant never has
// zero or two working tokens from the database's view; the registry follows
// at the next reconcile. the new token's created_by is the operator who made
// the tenant, as the schema requires a user.
func rotateProbeOTLPToken(ctx context.Context, d *pdb.DB) (string, int, error) {
	tok, err := otlptokens.Generate()
	if err != nil {
		return "", 0, err
	}
	revoked := 0
	err = d.WithSystem(ctx, func(tx pgx.Tx) error {
		var tenantID, createdBy uuid.UUID
		err := tx.QueryRow(ctx, `select id, created_by from cp.tenants where is_internal and slug = $1`, probeSlug).Scan(&tenantID, &createdBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("no internal probe tenant: run probe-tenant create first")
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `update cp.otlp_tokens set status = 'revoked', revoked_at = now()
			where tenant_id = $1 and status = 'active' returning id`, tenantID)
		if err != nil {
			return fmt.Errorf("revoke otlp tokens: %w", err)
		}
		var old []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			old = append(old, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var newID uuid.UUID
		if err := tx.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, environment, prefix, hash, created_by)
			values ($1, 'probe', '', $2, $3, $4) returning id`, tenantID, tok.Display, tok.Hash[:], createdBy).Scan(&newID); err != nil {
			return fmt.Errorf("insert otlp token: %w", err)
		}
		entries := []audit.Entry{{Actor: probeActor, Action: audit.OTLPTokenCreate, Target: audit.OTLPTokenTarget(newID),
			Detail: map[string]any{"tenant_id": tenantID, "prefix": tok.Display}}}
		for _, id := range old {
			entries = append(entries, audit.Entry{Actor: probeActor, Action: audit.OTLPTokenRevoke, Target: audit.OTLPTokenTarget(id),
				Detail: map[string]any{"tenant_id": tenantID, "status": audit.Change(otlptokens.StatusActive, otlptokens.StatusRevoked)}})
		}
		for _, e := range entries {
			if err := audit.InSystem(ctx, tx, e); err != nil {
				return err
			}
		}
		revoked = len(old)
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return tok.Plain, revoked, nil
}
