package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/controlplane/auth"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/lifecycle"
	"github.com/justmert/veristar/internal/controlplane/members"
	"github.com/justmert/veristar/internal/controlplane/otlptokens"
	"github.com/justmert/veristar/internal/controlplane/scfawards"
	"github.com/justmert/veristar/internal/controlplane/sites"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
)

const tenantUsage = `  tenant create (-owner-github-id N | -owner-github-login L) -name NAME -website URL
                -stellar-usage TEXT [-slug S] [-description TEXT] [-stellar-id ID ...]
                [-scf-round-url URL] [-program P -round R -category C -award-date YYYY-MM-DD]
                [-site-domain D -origin URL ... [-site-name NAME]]
                [-otlp-token-name NAME [-otlp-token-environment ENV]] [-approve] [-dry-run]
                    enroll a project on behalf of a console user who signed in
                    once: the user becomes its owner and accepts the terms at
                    their next sign-in. the values are checked as the console
                    checks them. prints the tenant id, the site id with its
                    script tag, and the otlp token once
  tenant invite (-tenant ID | -slug S) -github-login L [-role member|owner] [-dry-run]
                    add a person to a project as the console does: a member at
                    once when the github account has signed in, else an invite
                    that their first sign-in resolves
  tenant remove (-tenant ID | -slug S) -reason TEXT [-dry-run]
                    remove a project as the operator action does; the
                    reconciler purges it within 10 minutes
`

// tenantActor marks the audit rows of the tenant commands, named like
// probeActor
const tenantActor = "system:platformctl-tenant"

// errDryRun rolls back the transaction of a dry run
var errDryRun = errors.New("dry run")

func runTenant(ctx context.Context, args []string, env *config.Env, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: platformctl tenant create | invite | remove (platformctl help lists the flags)")
	}
	switch args[0] {
	case "create":
		o, err := parseCreate(args[1:], env)
		if err != nil {
			return err
		}
		var tr *controlplane.Tracker
		if o.site != nil {
			if tr, err = controlplane.LoadTracker(o.trackerDir, o.ingestOrigin); err != nil {
				return err
			}
		}
		d, err := openSystem(ctx, env)
		if err != nil {
			return err
		}
		defer d.Close()
		res, err := createTenant(ctx, d, o, tr)
		if err != nil {
			return err
		}
		return res.print(stdout)
	case "invite":
		o, err := parseInvite(args[1:])
		if err != nil {
			return err
		}
		users, err := githubLookup(env)
		if err != nil {
			return err
		}
		d, err := openSystem(ctx, env)
		if err != nil {
			return err
		}
		defer d.Close()
		res, err := inviteToTenant(ctx, d, users, o)
		if err != nil {
			return err
		}
		return res.print(stdout)
	case "remove":
		o, err := parseRemove(args[1:])
		if err != nil {
			return err
		}
		d, err := openSystem(ctx, env)
		if err != nil {
			return err
		}
		defer d.Close()
		sum, err := removeTenant(ctx, d, o)
		if err != nil {
			return err
		}
		return printRemoval(stdout, sum, o.dryRun)
	}
	return errors.New("usage: platformctl tenant create | invite | remove (platformctl help lists the flags)")
}

// stringList is a flag that may be given more than once
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// setFlags returns the names of the flags given on the command line, so an
// empty value given on purpose is still checked as the console checks it
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func optional(set map[string]bool, name, v string) *string {
	if !set[name] {
		return nil
	}
	return &v
}

// arrayIndex strips the index of a json path such as origins[2]
var arrayIndex = regexp.MustCompile(`\[[0-9]+\]$`)

// flagErr turns a 422 of the shared validators into an error that names
// the flags instead of the json paths of the http routes. paths maps each
// json path to its flag.
func flagErr(err error, paths map[string]string) error {
	var he *httpx.Error
	if !errors.As(err, &he) || len(he.Fields) == 0 {
		return err
	}
	var out []string
	for path, code := range he.Fields {
		name, ok := paths[arrayIndex.ReplaceAllString(path, "")]
		if !ok {
			name = path
		}
		out = append(out, name+" is "+strings.ReplaceAll(code, "_", " "))
	}
	sort.Strings(out)
	return errors.New("invalid flags: " + strings.Join(out, "; "))
}

var tenantPaths = map[string]string{
	"name": "-name", "slug": "-slug", "description": "-description",
	"enrollment": "-website", "enrollment.website": "-website",
	"enrollment.stellar_usage": "-stellar-usage", "enrollment.stellar_ids": "-stellar-id",
	"enrollment.scf_round_url": "-scf-round-url",
	"cohort":                   "-program, -round, -category or -award-date", "cohort.program": "-program",
	"cohort.round": "-round", "cohort.category": "-category", "cohort.award_date": "-award-date",
}

var sitePaths = map[string]string{"name": "-site-name", "domain": "-site-domain", "origins": "-origin"}

var tokenPaths = map[string]string{"name": "-otlp-token-name", "environment": "-otlp-token-environment"}

type siteInput struct {
	name    string
	domain  string
	origins []string
}

type tokenInput struct {
	name string
	env  string
}

type createOpts struct {
	ownerID      int64
	ownerLogin   string
	tenant       tenants.NewTenant
	site         *siteInput
	token        *tokenInput
	approve      bool
	dryRun       bool
	trackerDir   string
	ingestOrigin string
}

func parseCreate(args []string, env *config.Env) (createOpts, error) {
	fs := flag.NewFlagSet("tenant create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o createOpts
	fs.Int64Var(&o.ownerID, "owner-github-id", 0, "github id of the console user who becomes the owner")
	fs.StringVar(&o.ownerLogin, "owner-github-login", "", "github login of the console user who becomes the owner")
	name := fs.String("name", "", "project name")
	slug := fs.String("slug", "", "project slug; derived from the name when absent")
	desc := fs.String("description", "", "project description")
	website := fs.String("website", "", "the project's https website")
	usage := fs.String("stellar-usage", "", "how the project uses stellar")
	var ids stringList
	fs.Var(&ids, "stellar-id", "a stellar account or contract id; repeat for more")
	round := fs.String("scf-round-url", "", "link to the project's scf round")
	program := fs.String("program", "", "cohort program")
	cRound := fs.String("round", "", "cohort round")
	category := fs.String("category", "", "cohort category")
	award := fs.String("award-date", "", "cohort award date, YYYY-MM-DD")
	siteName := fs.String("site-name", "", "site name; the project name when absent")
	domain := fs.String("site-domain", "", "the site's domain")
	var origins stringList
	fs.Var(&origins, "origin", "an allowed origin of the site; repeat for more")
	tokName := fs.String("otlp-token-name", "", "create one otlp token with this name")
	tokEnv := fs.String("otlp-token-environment", "", "the otlp token's environment label")
	fs.BoolVar(&o.approve, "approve", false, "create the project active while enrollment needs approval")
	fs.BoolVar(&o.dryRun, "dry-run", false, "check and print what would be created, then roll back")
	fs.StringVar(&o.trackerDir, "tracker-releases", "", "tracker release directory; TRACKER_RELEASE_DIR when absent")
	fs.StringVar(&o.ingestOrigin, "ingest-origin", "", "the ingest origin of the script tag; INGEST_ORIGIN when absent")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 {
		return o, fmt.Errorf("tenant create takes flags only, not %q", fs.Arg(0))
	}
	set := setFlags(fs)
	switch {
	case set["owner-github-id"] == set["owner-github-login"]:
		return o, errors.New("tenant create needs exactly one of -owner-github-id and -owner-github-login")
	case set["owner-github-id"] && o.ownerID <= 0:
		return o, errors.New("-owner-github-id must be a positive github id")
	case set["owner-github-login"] && !members.ValidLogin(o.ownerLogin):
		return o, errors.New("-owner-github-login is not a valid github login")
	}

	in := tenants.NewTenantInput{
		Name: optional(set, "name", *name), Slug: optional(set, "slug", *slug),
		Description: optional(set, "description", *desc),
		Enrollment: &tenants.EnrollmentInput{
			Website: optional(set, "website", *website), StellarUsage: optional(set, "stellar-usage", *usage),
			SCFRoundURL: optional(set, "scf-round-url", *round),
		},
	}
	if len(ids) > 0 {
		in.Enrollment.StellarIDs = ids
	}
	if set["program"] || set["round"] || set["category"] || set["award-date"] {
		in.Cohort = &tenants.CohortInput{
			Program: optional(set, "program", *program), Round: optional(set, "round", *cRound),
			Category: optional(set, "category", *category), AwardDate: optional(set, "award-date", *award),
		}
	}
	var f tenants.Fields
	o.tenant = tenants.CheckNewTenant(&f, in, time.Now().UTC())
	errs := []error{flagErr(f.Err(), tenantPaths)}

	if set["site-domain"] || len(origins) > 0 || set["site-name"] {
		sn := o.tenant.Name
		if set["site-name"] {
			sn = *siteName
		}
		var sf tenants.Fields
		n, dom := sites.CheckNewSite(&sf, &sn, optional(set, "site-domain", *domain), origins)
		errs = append(errs, flagErr(sf.Err(), sitePaths))
		o.site = &siteInput{name: n, domain: dom, origins: origins}
		if o.trackerDir == "" {
			o.trackerDir = envOr(env, "TRACKER_RELEASE_DIR")
		}
		if o.ingestOrigin == "" {
			o.ingestOrigin = envOr(env, "INGEST_ORIGIN")
		}
		if o.trackerDir == "" || o.ingestOrigin == "" {
			errs = append(errs, errors.New("a site needs the tracker releases and the ingest origin for its script tag: "+
				"-tracker-releases or TRACKER_RELEASE_DIR, and -ingest-origin or INGEST_ORIGIN"))
		}
	}
	if set["otlp-token-name"] || set["otlp-token-environment"] {
		var tf tenants.Fields
		n, e := otlptokens.CheckNewToken(&tf, optional(set, "otlp-token-name", *tokName), optional(set, "otlp-token-environment", *tokEnv))
		errs = append(errs, flagErr(tf.Err(), tokenPaths))
		o.token = &tokenInput{name: n, env: e}
	}
	return o, errors.Join(errs...)
}

func envOr(env *config.Env, key string) string {
	if env == nil || !env.Has(key) {
		return ""
	}
	return env.String(key)
}

// owner is the console account that becomes the project's owner
type owner struct {
	ID       uuid.UUID
	GitHubID int64
	Login    string
}

func findOwner(ctx context.Context, tx pgx.Tx, ghID int64, login string) (owner, error) {
	rows, err := tx.Query(ctx, `select id, github_id, github_login from cp.users
		where deleted_at is null and github_id is not null
		  and (github_id = $1 or ($2 <> '' and lower(github_login) = lower($2)))`, ghID, login)
	if err != nil {
		return owner{}, err
	}
	found, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (owner, error) {
		var o owner
		err := r.Scan(&o.ID, &o.GitHubID, &o.Login)
		return o, err
	})
	if err != nil {
		return owner{}, err
	}
	who := fmt.Sprintf("github id %d", ghID)
	if login != "" {
		who = "github login " + login
	}
	switch len(found) {
	case 0:
		return owner{}, fmt.Errorf("no console account has %s. ask the person to sign in to the console once with "+
			"GitHub (Sign in with GitHub on the console's start page), then run this command again", who)
	case 1:
		return found[0], nil
	}
	return owner{}, fmt.Errorf("more than one console account has %s; use -owner-github-id", who)
}

type createResult struct {
	dryRun       bool
	TenantID     uuid.UUID
	Slug         string
	Status       string
	Owner        owner
	Approved     bool
	Cohort       bool
	SiteID       uuid.UUID
	SiteStatus   string
	OTLPToken    string
	OTLPPrefix   string
	TrackerVer   string
	ScriptTag    string
	hasSite      bool
	hasToken     bool
	scfProposals int
}

func (r createResult) print(w io.Writer) error {
	var b strings.Builder
	if r.dryRun {
		b.WriteString("dry_run=true\n")
	}
	fmt.Fprintf(&b, "tenant_id=%s\nslug=%s\nstatus=%s\nowner_user_id=%s\nowner_github_login=%s\n",
		r.TenantID, r.Slug, r.Status, r.Owner.ID, r.Owner.Login)
	b.WriteString("terms=accepted by the owner at their next sign-in\n")
	if r.Approved {
		b.WriteString("approved=true\n")
	}
	fmt.Fprintf(&b, "cohort_proposal=%t\nscf_proposals=%d\n", r.Cohort, r.scfProposals)
	if r.hasSite {
		fmt.Fprintf(&b, "site_id=%s\nsite_status=%s\ntracker_version=%s\n", r.SiteID, r.SiteStatus, r.TrackerVer)
		if !r.dryRun {
			fmt.Fprintf(&b, "script_tag=%s\n", r.ScriptTag)
		}
	}
	if r.hasToken {
		fmt.Fprintf(&b, "otlp_token_prefix=%s\n", r.OTLPPrefix)
		if !r.dryRun {
			fmt.Fprintf(&b, "otlp_token=%s\n", r.OTLPToken)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func createTenant(ctx context.Context, d *pdb.DB, o createOpts, tr *controlplane.Tracker) (createResult, error) {
	res := createResult{dryRun: o.dryRun, hasSite: o.site != nil, hasToken: o.token != nil}
	answers, err := o.tenant.Answers()
	if err != nil {
		return res, err
	}
	var tok otlptokens.Generated
	if o.token != nil {
		if tok, err = otlptokens.Generate(); err != nil {
			return res, err
		}
		res.OTLPToken, res.OTLPPrefix = tok.Plain, tok.Display
	}
	if o.site != nil {
		avail, err := tr.Available()
		if err != nil {
			return res, err
		}
		if len(avail) == 0 {
			return res, errors.New("every tracker version is revoked; release a new one first")
		}
		res.TrackerVer = avail[0].Version
	}
	err = d.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		ow, err := findOwner(ctx, tx, o.ownerID, o.ownerLogin)
		if err != nil {
			return err
		}
		res.Owner = ow
		raw, err := q.GetSetting(ctx, "enrollment_mode")
		if err != nil {
			return fmt.Errorf("read enrollment_mode: %w", err)
		}
		mode, err := tenants.ParseEnrollmentMode(raw)
		if err != nil {
			return err
		}
		status := "active"
		switch {
		case mode == tenants.ModeApproval && !o.approve:
			status = "pending"
		case mode == tenants.ModeOpen && o.approve:
			return errors.New("-approve applies only while enrollment needs approval; the enrollment mode is open")
		}
		if o.token != nil && status != "active" {
			return errors.New("the project would be pending; tokens can be created once it is active. " +
				"add -approve, or leave out the token and create it after approval")
		}
		t, err := q.EnrollTenant(ctx, systemq.EnrollTenantParams{
			Slug: o.tenant.Slug, Name: o.tenant.Name, Description: o.tenant.Description, Status: status,
			EnrollmentAnswers: answers, CreatedBy: uuid.NullUUID{UUID: ow.ID, Valid: true},
		})
		if err != nil {
			if tenants.IsUnique(err, "tenants_slug_key") {
				return fmt.Errorf("slug %s is taken; choose another with -slug", o.tenant.Slug)
			}
			return err
		}
		res.TenantID, res.Slug, res.Status = t.ID, t.Slug, t.Status
		if err := q.AddTenantMember(ctx, systemq.AddTenantMemberParams{TenantID: t.ID, UserID: ow.ID, Role: tenants.RoleOwner}); err != nil {
			return err
		}
		detail := map[string]any{
			"slug": t.Slug, "name": t.Name, "status": t.Status, "accepted_terms_version": nil,
			"enrollment": o.tenant.Enrollment, "owner_user_id": ow.ID, "owner_github_login": ow.Login,
		}
		if !o.tenant.Cohort.Empty() {
			if err := tenants.ProposeCohort(ctx, tx, t.ID, ow.ID, o.tenant.Cohort); err != nil {
				return err
			}
			detail["cohort_proposal"] = o.tenant.Cohort.AuditDetail()
			res.Cohort = true
		}
		entries := []audit.Entry{{Actor: tenantActor, Action: audit.TenantCreate, Target: audit.TenantTarget(t.ID), Detail: detail}}
		if o.approve {
			res.Approved = true
			entries = append(entries, audit.Entry{Actor: tenantActor, Action: lifecycle.AuditTenantApprove, Target: audit.TenantTarget(t.ID),
				Detail: map[string]any{"tenant_id": t.ID.String(), "status": audit.Change("pending", "active"),
					"enrollment_mode": mode, "reason": "approved at creation with platformctl tenant create -approve"}})
		}
		if o.site != nil {
			e, err := insertSite(ctx, tx, q, t.ID, t.Status, *o.site, &res)
			if err != nil {
				return err
			}
			entries = append(entries, e...)
		}
		if o.token != nil {
			var tokID uuid.UUID
			if err := tx.QueryRow(ctx, `insert into cp.otlp_tokens (tenant_id, name, environment, prefix, hash, created_by)
				values ($1, $2, $3, $4, $5, $6) returning id`, t.ID, o.token.name, o.token.env, tok.Display, tok.Hash[:], ow.ID).Scan(&tokID); err != nil {
				return fmt.Errorf("insert otlp token: %w", err)
			}
			entries = append(entries, audit.Entry{Actor: tenantActor, Action: audit.OTLPTokenCreate, Target: audit.OTLPTokenTarget(tokID),
				Detail: map[string]any{"tenant_id": t.ID, "name": o.token.name, "environment": o.token.env, "prefix": tok.Display}})
		}
		for _, e := range entries {
			if err := audit.InSystem(ctx, tx, e); err != nil {
				return err
			}
		}
		// the matching the console runs after an enrollment and a site
		// create, in the same transaction
		af, err := scfawards.Autofill(ctx, tx, scfawards.ActorMatch, []uuid.UUID{t.ID})
		if err != nil {
			return err
		}
		res.scfProposals = af.Proposed
		if o.dryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && (!o.dryRun || !errors.Is(err, errDryRun)) {
		return createResult{}, err
	}
	if o.site != nil && !o.dryRun {
		tag, _, err := tr.Snippet(res.SiteID, res.TrackerVer)
		if err != nil {
			return createResult{}, err
		}
		res.ScriptTag = tag
	}
	return res, nil
}

// insertSite adds the site as the site route does: active in an active
// project unless its domain matches a removed project's site, which sends
// it to review (contract 5.6)
func insertSite(ctx context.Context, tx pgx.Tx, q *systemq.Queries, tenantID uuid.UUID, tenantStatus string, s siteInput, res *createResult) ([]audit.Entry, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	qid, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	vb := make([]byte, 32)
	if _, err := rand.Read(vb); err != nil {
		return nil, err
	}
	var match *systemq.ReviewMatchRemovedDomainRow
	m, err := q.ReviewMatchRemovedDomain(ctx, systemq.ReviewMatchRemovedDomainParams{TenantID: tenantID, Domain: s.domain})
	switch {
	case err == nil:
		match = &m
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	status := sites.StatusPending
	if tenantStatus == "active" && match == nil {
		status = sites.StatusActive
	}
	if _, err := tx.Exec(ctx, `insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
		values ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, tenantID, s.name, s.domain, s.origins, qid, status, base64.RawURLEncoding.EncodeToString(vb)); err != nil {
		return nil, fmt.Errorf("insert site: %w", err)
	}
	res.SiteID, res.SiteStatus = id, status
	out := []audit.Entry{{Actor: tenantActor, Action: audit.SiteCreate, Target: audit.SiteTarget(id),
		Detail: map[string]any{"tenant_id": tenantID, "name": s.name, "domain": s.domain,
			"origins": s.origins, "status": status, "quarantine_website_id": qid}}}
	if match != nil {
		out = append(out, audit.Entry{Actor: tenantActor, Action: sites.ActionReviewQueue, Target: audit.SiteTarget(id),
			Detail: map[string]any{"tenant_id": tenantID, "domain": s.domain,
				"matched_domain": match.Domain, "matched_tenant_id": match.TenantID}})
	}
	return out, nil
}

// tenantRef names a project by id or by slug
type tenantRef struct {
	id   uuid.UUID
	slug string
}

func parseRef(set map[string]bool, id, slug string) (tenantRef, error) {
	switch {
	case set["tenant"] == set["slug"]:
		return tenantRef{}, errors.New("name the project with exactly one of -tenant and -slug")
	case set["tenant"]:
		u, err := uuid.Parse(id)
		if err != nil || len(id) != 36 {
			return tenantRef{}, errors.New("-tenant must be a tenant id (a uuid)")
		}
		return tenantRef{id: u}, nil
	}
	return tenantRef{slug: slug}, nil
}

// resolve returns the id of the project, refusing internal ones
func (r tenantRef) resolve(ctx context.Context, tx pgx.Tx) (uuid.UUID, error) {
	var id uuid.UUID
	var internal bool
	err := tx.QueryRow(ctx, `select id, is_internal from cp.tenants where id = $1 or ($2 <> '' and slug = $2)`,
		r.id, r.slug).Scan(&id, &internal)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && internal) {
		return uuid.Nil, fmt.Errorf("no project %s (internal projects are refused)", r)
	}
	return id, err
}

func (r tenantRef) String() string {
	if r.slug != "" {
		return "with slug " + r.slug
	}
	return "with id " + r.id.String()
}

type inviteOpts struct {
	ref    tenantRef
	login  string
	role   string
	dryRun bool
}

func parseInvite(args []string) (inviteOpts, error) {
	fs := flag.NewFlagSet("tenant invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("tenant", "", "tenant id")
	slug := fs.String("slug", "", "tenant slug")
	login := fs.String("github-login", "", "github login of the person")
	role := fs.String("role", tenants.RoleMember, "member or owner")
	dry := fs.Bool("dry-run", false, "check and print what would be created, then roll back")
	if err := fs.Parse(args); err != nil {
		return inviteOpts{}, err
	}
	if fs.NArg() != 0 {
		return inviteOpts{}, fmt.Errorf("tenant invite takes flags only, not %q", fs.Arg(0))
	}
	set := setFlags(fs)
	ref, err := parseRef(set, *id, *slug)
	if err != nil {
		return inviteOpts{}, err
	}
	switch {
	case !set["github-login"]:
		return inviteOpts{}, errors.New("-github-login is required")
	case !members.ValidLogin(*login):
		return inviteOpts{}, errors.New("-github-login is not a valid github login")
	case *role != tenants.RoleOwner && *role != tenants.RoleMember:
		return inviteOpts{}, errors.New("-role must be member or owner")
	}
	return inviteOpts{ref: ref, login: *login, role: *role, dryRun: *dry}, nil
}

// githubLookup resolves logins with the console's oauth app, as the member
// route does
func githubLookup(env *config.Env) (members.UserLookup, error) {
	id := env.String("GITHUB_CLIENT_ID")
	secret := env.Secret("GITHUB_CLIENT_SECRET", 1)
	if err := env.Err(); err != nil {
		return nil, fmt.Errorf("tenant invite resolves the login with the console's github app: %w", err)
	}
	return auth.NewUserLookup(auth.UserLookupConfig{ClientID: id, ClientSecret: secret, Endpoints: auth.LoadEndpoints(env)})
}

type inviteResult struct {
	dryRun   bool
	Kind     string
	TenantID uuid.UUID
	UserID   uuid.UUID
	InviteID uuid.UUID
	Login    string
	GitHubID int64
	Role     string
	Expires  time.Time
}

func (r inviteResult) print(w io.Writer) error {
	var b strings.Builder
	if r.dryRun {
		b.WriteString("dry_run=true\n")
	}
	fmt.Fprintf(&b, "kind=%s\ntenant_id=%s\ngithub_login=%s\ngithub_id=%d\nrole=%s\n", r.Kind, r.TenantID, r.Login, r.GitHubID, r.Role)
	if r.Kind == "member" {
		fmt.Fprintf(&b, "user_id=%s\n", r.UserID)
	} else {
		fmt.Fprintf(&b, "invite_id=%s\nexpires_at=%s\n", r.InviteID, r.Expires.UTC().Format(time.RFC3339))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// inviteToTenant adds the person the way POST /api/tenants/{id}/members
// does: an account that signed in becomes a member at once, anyone else
// gets an invite bound to the github account id
func inviteToTenant(ctx context.Context, d *pdb.DB, users members.UserLookup, o inviteOpts) (inviteResult, error) {
	gh, err := users.Lookup(ctx, o.login)
	if errors.Is(err, auth.ErrGitHubUserNotFound) {
		return inviteResult{}, fmt.Errorf("no github user has the login %s", o.login)
	}
	if err != nil {
		return inviteResult{}, fmt.Errorf("github could not be reached to look up the login: %w", err)
	}
	res := inviteResult{dryRun: o.dryRun, Login: gh.Login, GitHubID: gh.ID, Role: o.role}
	err = d.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		id, err := o.ref.resolve(ctx, tx)
		if err != nil {
			return err
		}
		res.TenantID = id
		var status string
		if err := tx.QueryRow(ctx, `select status from cp.tenants where id = $1 for update`, id).Scan(&status); err != nil {
			return err
		}
		if status == tenants.StatusRemoved {
			return fmt.Errorf("project %s is removed", id)
		}
		u, err := q.FindUserByGitHubID(ctx, &gh.ID)
		switch {
		case err == nil:
			if err := q.RefreshUserLogin(ctx, systemq.RefreshUserLoginParams{GithubLogin: &gh.Login, GithubID: &gh.ID}); err != nil {
				return err
			}
			var member bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from cp.tenant_members where tenant_id = $1 and user_id = $2)`,
				id, u.ID).Scan(&member); err != nil {
				return err
			}
			if member {
				return fmt.Errorf("%s is already a member", gh.Login)
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		known := err == nil
		if _, err := tx.Exec(ctx, `delete from cp.invites where tenant_id = $1 and github_id = $2 and expires_at <= now()`, id, gh.ID); err != nil {
			return err
		}
		var total int
		if err := tx.QueryRow(ctx, `select (select count(*) from cp.tenant_members where tenant_id = $1)
			+ (select count(*) from cp.invites where tenant_id = $1 and expires_at > now())`, id).Scan(&total); err != nil {
			return err
		}
		if total >= members.MaxMembersAndInvites {
			return fmt.Errorf("a project has at most %d members and open invites", members.MaxMembersAndInvites)
		}
		if known {
			res.Kind, res.UserID = "member", u.ID
			if err := q.AddTenantMember(ctx, systemq.AddTenantMemberParams{TenantID: id, UserID: u.ID, Role: o.role}); err != nil {
				return err
			}
			if err := audit.InSystem(ctx, tx, audit.Entry{Actor: tenantActor, Action: audit.MemberAdd, Target: audit.UserTarget(u.ID),
				Detail: map[string]any{"tenant_id": id, "github_login": gh.Login, "role": o.role}}); err != nil {
				return err
			}
		} else {
			// created_by names a user; the oldest owner stands in, as the
			// person the invite comes from
			var by uuid.UUID
			err := tx.QueryRow(ctx, `select user_id from cp.tenant_members where tenant_id = $1 and role = 'owner'
				order by created_at, user_id limit 1`, id).Scan(&by)
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("the project has no owner to send the invite from")
			}
			if err != nil {
				return err
			}
			res.Kind = "invite"
			err = tx.QueryRow(ctx, `insert into cp.invites (tenant_id, github_id, github_login, role, created_by, expires_at)
				values ($1, $2, $3, $4, $5, $6) returning id, expires_at`,
				id, gh.ID, gh.Login, o.role, by, time.Now().Add(members.InviteLifetime)).Scan(&res.InviteID, &res.Expires)
			if tenants.IsUnique(err, "invites_tenant_github_idx") {
				return fmt.Errorf("an open invite for %s exists", gh.Login)
			}
			if err != nil {
				return err
			}
			if err := audit.InSystem(ctx, tx, audit.Entry{Actor: tenantActor, Action: audit.InviteCreate, Target: audit.InviteTarget(res.InviteID),
				Detail: map[string]any{"tenant_id": id, "role": o.role, "github_id": gh.ID, "github_login": gh.Login}}); err != nil {
				return err
			}
		}
		if o.dryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && (!o.dryRun || !errors.Is(err, errDryRun)) {
		return inviteResult{}, err
	}
	return res, nil
}

type removeOpts struct {
	ref    tenantRef
	reason string
	dryRun bool
}

func parseRemove(args []string) (removeOpts, error) {
	fs := flag.NewFlagSet("tenant remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("tenant", "", "tenant id")
	slug := fs.String("slug", "", "tenant slug")
	reason := fs.String("reason", "", "the reason, shown to the project's members")
	dry := fs.Bool("dry-run", false, "run the removal and roll it back")
	if err := fs.Parse(args); err != nil {
		return removeOpts{}, err
	}
	if fs.NArg() != 0 {
		return removeOpts{}, fmt.Errorf("tenant remove takes flags only, not %q", fs.Arg(0))
	}
	set := setFlags(fs)
	ref, err := parseRef(set, *id, *slug)
	if err != nil {
		return removeOpts{}, err
	}
	r, err := lifecycle.CheckReason(optional(set, "reason", *reason), true)
	if err != nil {
		return removeOpts{}, flagErr(err, map[string]string{"reason": "-reason"})
	}
	return removeOpts{ref: ref, reason: *r, dryRun: *dry}, nil
}

// removeTenant runs the operator removal of contract 5.5 with platformctl
// as the actor. the reconcile the console triggers at once is left to the
// jobs task, which runs every 10 minutes and purges the project.
func removeTenant(ctx context.Context, d *pdb.DB, o removeOpts) (lifecycle.TenantSummary, error) {
	var id uuid.UUID
	if err := d.WithSystem(ctx, func(tx pgx.Tx) error {
		var err error
		id, err = o.ref.resolve(ctx, tx)
		return err
	}); err != nil {
		return lifecycle.TenantSummary{}, err
	}
	acts, err := lifecycle.NewActions(d, func(context.Context, uuid.UUID) {})
	if err != nil {
		return lifecycle.TenantSummary{}, err
	}
	sum, err := acts.TenantActionAs(ctx, tenantActor, id, lifecycle.Remove, &o.reason, o.dryRun)
	var he *httpx.Error
	if errors.As(err, &he) {
		if he.Status == 404 {
			return sum, fmt.Errorf("no project %s (internal projects are refused)", o.ref)
		}
		return sum, errors.New(he.Message)
	}
	return sum, err
}

func printRemoval(w io.Writer, s lifecycle.TenantSummary, dry bool) error {
	var b strings.Builder
	if dry {
		b.WriteString("dry_run=true\n")
	}
	fmt.Fprintf(&b, "tenant_id=%s\nslug=%s\nname=%s\nstatus=%s\nsites=%d\n", s.ID, s.Slug, s.Name, s.Status, s.SiteCount)
	if !dry {
		b.WriteString("purge=the jobs reconcile task purges the project within 10 minutes\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
