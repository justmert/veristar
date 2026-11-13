package verifydomains_test

import (
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/domaincheck"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
	"github.com/justmert/veristar/internal/jobs/scheduler"
	"github.com/justmert/veristar/internal/jobs/verifydomains"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/netguard"
	"github.com/justmert/veristar/tests/fixtures/domainfix"
)

const zone = "stellar-verify.test"

// files serves host+path bodies; anything else is 404.
type files struct {
	mu sync.Mutex
	m  map[string]string
}

func (f *files) set(hostPath, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[hostPath] = body
}

func (f *files) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	b, ok := f.m[r.Host+r.URL.Path]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte(b))
}

type siteRow struct {
	verification string
	method       *string
	lastErr      *string
	tomlStatus   *string
	checkedAt    *time.Time
}

func TestRun(t *testing.T) {
	ctx := t.Context()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleJobs, pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "verifydomains-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	srv := jobstest.NATS(t)
	_, js := jobstest.Connect(t, srv.URL)
	kv, err := js.KeyValue(ctx, natsx.BucketLocks)
	require.NoError(t, err)
	lock, err := natsx.NewLock(kv, natsx.LockJobs, "verifydomains-test")
	require.NoError(t, err)
	_, err = lock.Acquire(ctx)
	require.NoError(t, err)
	leader, err := scheduler.NewLeader(lock, d)
	require.NoError(t, err)

	// real dns and https servers for the test zone
	dns := domainfix.StartDNS(t)
	fs := &files{m: map[string]string{}}
	https := domainfix.StartHTTPS(t, fs, zone, "*."+zone)
	loop := netip.MustParseAddr("127.0.0.1")
	for _, l := range []string{"dns", "file", "none", "gone", "fresh", "removedtenant"} {
		dns.SetAddrs(l+"."+zone, loop)
	}
	g, err := netguard.NewForTest(dns.Resolver(), netguard.TestOptions{
		Allow: []netip.Prefix{netip.PrefixFrom(loop, 32)}, RootCAs: https.Roots, Port: https.Port,
	})
	require.NoError(t, err)
	checker := &domaincheck.Checker{Guard: g, Resolver: dns.Resolver()}

	sys := func(fn func(pgx.Tx) error) { require.NoError(t, d.WithSystem(ctx, fn)) }
	var owner uuid.UUID
	sys(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `insert into cp.users (github_id, github_login) values (9200001, 'vd-owner') returning id`).Scan(&owner)
	})
	tenant := func(status string) uuid.UUID {
		var id uuid.UUID
		sys(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `insert into cp.tenants (slug, name, status, created_by) values ($1, 'P', $2, $3) returning id`,
				"vd-"+uuid.NewString()[:8], status, owner).Scan(&id)
		})
		return id
	}
	site := func(tenant uuid.UUID, label, status, tok string) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		sys(func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status, verify_token)
				values ($1, $2, 'S', $3, $4, $5, $6, $7)`,
				id, tenant, label+"."+zone, []string{"https://" + label + "." + zone}, uuid.Must(uuid.NewV7()), status, tok)
			return err
		})
		return id
	}
	read := func(id uuid.UUID) siteRow {
		var r siteRow
		var verifiedAt, lastAt *time.Time
		sys(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select verify_method, verified_at, last_verify_at, last_verify_error,
				stellar_toml->>'status', stellar_toml_checked_at from cp.sites where id = $1`, id).
				Scan(&r.method, &verifiedAt, &lastAt, &r.lastErr, &r.tomlStatus, &r.checkedAt)
		})
		r.verification = domaincheck.Verification(verifiedAt, lastAt, r.lastErr)
		return r
	}
	audits := func(action string) int {
		var n int
		sys(func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select count(*) from cp.audit_log where action = $1 and actor = $2`, action, verifydomains.Actor).Scan(&n)
		})
		return n
	}

	active, pending, removedT := tenant("active"), tenant("pending"), tenant("removed")
	viaDNS := site(active, "dns", "active", "token-dns")
	viaFile := site(pending, "file", "pending", "token-file")
	none := site(active, "none", "active", "token-none")
	gone := site(active, "gone", "removed", "token-gone")
	fresh := site(active, "fresh", "active", "token-fresh")
	ofRemoved := site(removedT, "removedtenant", "removed", "token-rt")
	sys(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update cp.sites set last_verify_at = now(), stellar_toml_checked_at = now(),
			last_verify_error = 'file_not_found', stellar_toml = '{"status":"not_found"}' where id = $1`, fresh)
		return err
	})
	dns.SetTXT("_veristar.dns."+zone, []string{domaincheck.DNSValue("token-dns")})
	fs.set("file."+zone+domaincheck.FilePath, "token-file\n")
	valid, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "stellartoml", "valid.toml"))
	require.NoError(t, err)
	fs.set("dns."+zone+domaincheck.StellarTomlPath, string(valid))

	now := time.Now()
	task, err := verifydomains.New(checker, func() time.Time { return now }, slog.New(slog.DiscardHandler), prometheus.NewRegistry())
	require.NoError(t, err)

	res, err := task.Run(ctx, leader.SystemTx)
	require.NoError(t, err)
	require.Equal(t, verifydomains.Result{Sites: 3}, res, "dns, file and none; not fresh, a removed site or a removed tenant's site")

	r := read(viaDNS)
	require.Equal(t, "verified", r.verification)
	require.Equal(t, "dns", *r.method)
	require.Equal(t, "found", *r.tomlStatus)
	r = read(viaFile)
	require.Equal(t, "verified", r.verification, "a pending tenant's site is checked too")
	require.Equal(t, "file", *r.method)
	require.Equal(t, "not_found", *r.tomlStatus)
	r = read(none)
	require.Equal(t, "unverified", r.verification)
	require.Equal(t, domaincheck.CodeFileNotFound, *r.lastErr)
	for _, id := range []uuid.UUID{gone, ofRemoved} {
		r = read(id)
		require.Equal(t, "unchecked", r.verification)
		require.Nil(t, r.checkedAt)
	}
	require.Equal(t, "not_found", *read(fresh).tomlStatus)
	require.Equal(t, 3, audits(domaincheck.ActionVerify))
	require.Equal(t, 3, audits(domaincheck.ActionStellarToml))

	// a second run finds nothing due
	res, err = task.Run(ctx, leader.SystemTx)
	require.NoError(t, err)
	require.Equal(t, 0, res.Sites)

	// a day later every live site is due; unchanged outcomes add no audit
	// rows, a changed one does
	now = now.Add(25 * time.Hour)
	dns.SetTXT("_veristar.dns."+zone, []string{domaincheck.DNSValue("rotated")})
	res, err = task.Run(ctx, leader.SystemTx)
	require.NoError(t, err)
	require.Equal(t, 4, res.Sites)
	r = read(viaDNS)
	require.Equal(t, "unverified", r.verification, "the dns record no longer matches and there is no file")
	require.Equal(t, "dns", *r.method, "the last success is kept")
	require.Equal(t, 4, audits(domaincheck.ActionVerify), "only the dns site changed")
	require.Equal(t, 3, audits(domaincheck.ActionStellarToml), "fresh stays not_found")

	// a fenced-off holder writes nothing
	require.NoError(t, d.WithRole(ctx, pdb.RoleJobs, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update cp.leader_fence set epoch = epoch + 1 where name = 'jobs'`)
		return err
	}))
	now = now.Add(25 * time.Hour)
	_, err = task.Run(ctx, leader.SystemTx)
	require.ErrorIs(t, err, natsx.ErrFenced)
}
