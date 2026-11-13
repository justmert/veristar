package db_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/db"
	"github.com/justmert/stellar-analytics/internal/config"
	pdb "github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/dbtest"
	"github.com/justmert/stellar-analytics/tests/fixtures/tlsca"
)

// TestTLSOnlyServer runs migrations, role logins and the role pools against
// a real postgresql that admits hostssl lines only (the pg_hba the core role
// deploys), with sslmode verify-full and the internal ca from config. it then
// proves the three ways a connection could end up in clear or with the wrong
// server are refused: plain text, a foreign ca, and a certificate for
// another name.
func TestTLSOnlyServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ca := tlsca.New(t, "sa-test-ca")
	dir := t.TempDir()
	server := ca.Issue(t, "postgres", "localhost", "127.0.0.1")
	caFile, _, _ := tlsca.Files(t, dir, ca, server)
	tp, err := dbtest.Start(ctx, dbtest.WithTLS(dbtest.ServerTLS{CertPEM: server.CertPEM, KeyPEM: server.KeyPEM, CAFile: caFile}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Terminate(context.Background()) })
	require.Equal(t, "verify-full", tp.Env["PLATFORM_DB_SSLMODE"])

	acfg, err := pdb.LoadAdminConfig(tp.Config("platform"))
	require.NoError(t, err)
	require.NotNil(t, acfg.Server.TLS, "the ca must reach the server settings")
	_, err = pdb.Migrate(ctx, acfg, db.Migrations(), quietLog())
	require.NoError(t, err)
	rcfg, err := pdb.LoadRolesConfig(tp.Config("platform"))
	require.NoError(t, err)
	require.NoError(t, pdb.SetRolePasswords(ctx, rcfg))
	pcfg, err := pdb.LoadPoolConfig(tp.Config("platform"), pdb.RoleCPSystem)
	require.NoError(t, err)
	d, err := pdb.Open(ctx, pcfg, "tls-test")
	require.NoError(t, err)
	defer d.Close()
	pool, err := d.Pool(pdb.RoleCPSystem)
	require.NoError(t, err)
	var ssl bool
	var version string
	require.NoError(t, pool.QueryRow(ctx, `select ssl, version from pg_stat_ssl where pid = pg_backend_pid()`).Scan(&ssl, &version))
	require.True(t, ssl, "the pool connection is not encrypted")
	require.Equal(t, "TLSv1.3", version)

	// what the server does with a client that does not ask for tls
	base := "host=" + tp.Host + " port=" + tp.Env["PLATFORM_DB_PORT"] + " dbname=platform user=postgres password=" + tp.Env["PLATFORM_DB_ADMIN_PASSWORD"]
	_, err = pgx.Connect(ctx, base+" sslmode=disable")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no encryption", "a plain text login must be refused by pg_hba")

	// the client refuses a server whose certificate another ca signed
	other := tlsca.New(t, "other-ca")
	otherCA, _, _ := tlsca.Files(t, t.TempDir(), other, other.Issue(t, "x", "x"))
	_, err = pgx.Connect(ctx, base+" sslmode=verify-full sslrootcert="+otherCA)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate signed by unknown authority")

	// and a server whose certificate does not carry the name dialed: the
	// name check of verify-full runs against the tls server name, which pgx
	// takes from host; here it is set to a name the certificate lacks
	cc, err := pgx.ParseConfig(base + " sslmode=verify-full sslrootcert=" + caFile)
	require.NoError(t, err)
	require.NotNil(t, cc.TLSConfig)
	cc.TLSConfig.ServerName = "haproxy"
	_, err = pgx.ConnectConfig(ctx, cc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "certificate is valid for")

	// config refuses to load a ca with a mode that would ignore it
	env := maps.Clone(tp.EnvFor("platform"))
	env["PLATFORM_DB_SSLMODE"] = "disable"
	_, err = pdb.LoadAdminConfig(config.FromMap(env))
	require.ErrorContains(t, err, "PLATFORM_DB_SSLMODE must be verify-full")
}
