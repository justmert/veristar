package domaincheck_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/controlplane/domaincheck"
	"github.com/justmert/stellar-analytics/internal/netguard"
)

const token = "Zm9vYmFyYmF6cXV4cXV1eGNvcmdlZ3JhdWx0Z2FycGx5" // 43 characters like a real one

func TestCheckDNS(t *testing.T) {
	t.Parallel()
	n := newNet(t, &tokenFiles{})
	c := n.checker(t)

	n.dns.SetTXT("_stellar-analytics.ok."+zone, []string{"v=spf1 -all"}, []string{domaincheck.DNSValue(token)})
	require.Empty(t, c.CheckDNS(t.Context(), "ok."+zone, token))

	// one record split into several strings is joined before comparing
	v := domaincheck.DNSValue(token)
	n.dns.SetTXT("_stellar-analytics.split."+zone, []string{v[:10], v[10:]})
	require.Empty(t, c.CheckDNS(t.Context(), "split."+zone, token))

	n.dns.SetTXT("_stellar-analytics.wrong."+zone, []string{domaincheck.DNSValue("another-token")})
	require.Equal(t, domaincheck.CodeDNSMismatch, c.CheckDNS(t.Context(), "wrong."+zone, token))

	// a prefix match is not enough
	n.dns.SetTXT("_stellar-analytics.prefix."+zone, []string{domaincheck.DNSValue(token) + "x"})
	require.Equal(t, domaincheck.CodeDNSMismatch, c.CheckDNS(t.Context(), "prefix."+zone, token))

	require.Equal(t, domaincheck.CodeDNSNoRecord, c.CheckDNS(t.Context(), "none."+zone, token))
	// the name exists but holds no txt record
	n.dns.SetAddrs("_stellar-analytics.empty."+zone, loopback)
	require.Equal(t, domaincheck.CodeDNSNoRecord, c.CheckDNS(t.Context(), "empty."+zone, token))
}

func TestCheckFile(t *testing.T) {
	t.Parallel()
	files := &tokenFiles{}
	n := newNet(t, files, "ok", "spaced", "wrong", "big", "absent")
	c := n.checker(t)
	files.set("ok."+zone, token)
	files.set("spaced."+zone, token+"\r\n  \n")
	files.set("wrong."+zone, "another-token")
	files.set("big."+zone, token+strings.Repeat(" ", domaincheck.MaxFileBytes))

	require.Empty(t, c.CheckFile(t.Context(), "ok."+zone, token))
	require.Empty(t, c.CheckFile(t.Context(), "spaced."+zone, token))
	require.Equal(t, domaincheck.CodeFileMismatch, c.CheckFile(t.Context(), "wrong."+zone, token))
	require.Equal(t, domaincheck.CodeFileTooLarge, c.CheckFile(t.Context(), "big."+zone, token))
	require.Equal(t, domaincheck.CodeFileNotFound, c.CheckFile(t.Context(), "absent."+zone, token))

	// the guard's refusal is the stored code
	files.set("inside."+zone, token)
	n.dns.SetAddrs("inside."+zone, loopback)
	strict := &domaincheck.Checker{Guard: n.guard(t, false), Resolver: n.dns.Resolver()}
	require.Equal(t, netguard.CodeRefusedAddress, strict.CheckFile(t.Context(), "inside."+zone, token))
}

func TestVerifyOrder(t *testing.T) {
	t.Parallel()
	files := &tokenFiles{}
	n := newNet(t, files, "both", "fileonly", "neither")
	c := n.checker(t)
	n.dns.SetTXT("_stellar-analytics.both."+zone, []string{domaincheck.DNSValue(token)})
	files.set("both."+zone, token)
	files.set("fileonly."+zone, token)

	o, err := c.Verify(t.Context(), "both."+zone, token, "")
	require.NoError(t, err)
	require.True(t, o.OK)
	require.Equal(t, domaincheck.MethodDNS, o.Method)
	require.Equal(t, map[string]string{"dns": "ok"}, o.Results, "the first success wins, the file is not fetched")

	o, err = c.Verify(t.Context(), "fileonly."+zone, token, "")
	require.NoError(t, err)
	require.True(t, o.OK)
	require.Equal(t, domaincheck.MethodFile, o.Method)
	require.Equal(t, map[string]string{"dns": domaincheck.CodeDNSNoRecord, "file": "ok"}, o.Results)

	o, err = c.Verify(t.Context(), "neither."+zone, token, "")
	require.NoError(t, err)
	require.False(t, o.OK)
	require.Equal(t, domaincheck.CodeFileNotFound, o.Error, "the file code is stored when both were tried")

	o, err = c.Verify(t.Context(), "fileonly."+zone, token, domaincheck.MethodDNS)
	require.NoError(t, err)
	require.False(t, o.OK)
	require.Equal(t, domaincheck.CodeDNSNoRecord, o.Error)
	require.Len(t, o.Results, 1)

	_, err = c.Verify(t.Context(), "fileonly."+zone, token, "smtp")
	require.Error(t, err)
}
