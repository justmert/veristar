package gateway

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/integrity"
)

// networkTestDir is the directory of DBIP_MMDB, where make geo-fetch also
// writes the network data.
func networkTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Dir(geoTestFile(t))
	if _, err := os.Stat(filepath.Join(dir, NetworkASNFile)); err != nil {
		t.Skipf("no %s beside DBIP_MMDB: run make geo-fetch", NetworkASNFile)
	}
	return dir
}

func firstTorExit(t *testing.T, dir string) ClientIP {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, NetworkTorFile))
	require.NoError(t, err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	require.True(t, sc.Scan())
	ip, err := ParseClientIP(sc.Text())
	require.NoError(t, err)
	return ip
}

func mustIP(t *testing.T, s string) ClientIP {
	t.Helper()
	ip, err := ParseClientIP(s)
	require.NoError(t, err)
	return ip
}

func TestNetworkClassifiesFromOpenData(t *testing.T) {
	dir := networkTestDir(t)
	n, err := OpenNetwork(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = n.Close() })

	for _, tc := range []struct {
		addr  string
		asn   uint32
		class integrity.NetClass
	}{
		// cloudflare and amazon are hosting networks
		{"1.1.1.1", 13335, integrity.NetHosting},
		{"2606:4700:4700::1111", 13335, integrity.NetHosting},
		// deutsche telekom and comcast are access networks
		{"80.128.0.1", 3320, integrity.NetAccess},
		// private ranges have no autonomous system
		{"10.1.2.3", 0, integrity.NetUnknown},
		{"fd00::1", 0, integrity.NetUnknown},
	} {
		c, asn, err := n.Classify(mustIP(t, tc.addr))
		require.NoError(t, err, tc.addr)
		require.Equal(t, tc.asn, asn, tc.addr)
		require.Equal(t, tc.class, c, tc.addr)
	}

	exit := firstTorExit(t, dir)
	c, _, err := n.Classify(exit)
	require.NoError(t, err)
	require.Equal(t, integrity.NetAnonymiser, c, "a published tor exit is an anonymiser")

	_, _, err = n.Classify(ClientIP{})
	require.Error(t, err)
	require.False(t, n.BuildTime().IsZero())
}

// copyNetwork makes a writable copy of the network data in a temp dir.
func copyNetwork(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	for _, f := range []string{NetworkASNFile, NetworkClassesFile, NetworkTorFile} {
		b, err := os.ReadFile(filepath.Join(src, f))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, f), b, 0o600))
	}
	return dst
}

func TestNetworkRefusesBrokenFiles(t *testing.T) {
	src := networkTestDir(t)
	for _, tc := range []struct {
		name, file, body string
	}{
		{"classes without header", NetworkClassesFile, "13335,hosting\n"},
		{"classes with an unknown class", NetworkClassesFile, "asn,class\n13335,vpn\n"},
		{"classes naming anonymisers", NetworkClassesFile, "asn,class\n13335,anonymiser\n"},
		{"classes with no rows", NetworkClassesFile, "asn,class\n"},
		{"tor list with a non address", NetworkTorFile, "not-an-address\n"},
		{"empty tor list", NetworkTorFile, ""},
		{"asn file of another type", NetworkASNFile, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyNetwork(t, src)
			if tc.file == NetworkASNFile {
				// the country file opens as an mmdb but has another type
				b, err := os.ReadFile(geoTestFile(t))
				require.NoError(t, err)
				tc.body = string(b)
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.body), 0o600))
			_, err := OpenNetwork(dir)
			require.Error(t, err)
			// an error never quotes an address from the file
			require.NotContains(t, err.Error(), "not-an-address")
		})
	}
}

func TestNetworkReloadKeepsDataOnFailure(t *testing.T) {
	dir := copyNetwork(t, networkTestDir(t))
	n, err := OpenNetwork(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = n.Close() })

	// cloudflare reclassified as access, as a new month's data could
	require.NoError(t, os.WriteFile(filepath.Join(dir, NetworkClassesFile), []byte("asn,class\n13335,access\n"), 0o600))
	require.NoError(t, n.Reload())
	c, _, err := n.Classify(mustIP(t, "1.1.1.1"))
	require.NoError(t, err)
	require.Equal(t, integrity.NetAccess, c)
	// an address whose system is no longer listed is unknown
	c, _, err = n.Classify(mustIP(t, "80.128.0.1"))
	require.NoError(t, err)
	require.Equal(t, integrity.NetUnknown, c)

	require.NoError(t, os.WriteFile(filepath.Join(dir, NetworkTorFile), nil, 0o600))
	require.Error(t, n.Reload())
	c, _, err = n.Classify(mustIP(t, "1.1.1.1"))
	require.NoError(t, err)
	require.Equal(t, integrity.NetAccess, c, "a failed reload keeps the loaded data")
}
