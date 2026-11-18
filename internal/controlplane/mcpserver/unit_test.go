package mcpserver

import (
	"crypto/rand"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSecrets(t *testing.T) {
	for _, prefix := range []string{KeyPrefix, AccessTokenPrefix, RefreshTokenPrefix, codePrefix} {
		plain, hash, err := newSecret(prefix)
		require.NoError(t, err)
		require.Len(t, plain, len(prefix)+40)
		got, ok := parseSecret(plain, prefix)
		require.True(t, ok)
		require.Equal(t, hash, got)
		// any other prefix, length or alphabet is refused before a lookup
		for _, bad := range []string{plain + "x", plain[:len(plain)-1], strings.Replace(plain, prefix, "sa_op_", 1),
			prefix + strings.Repeat("-", 40), "", prefix} {
			_, ok := parseSecret(bad, prefix)
			require.False(t, ok, bad)
		}
	}
	// a key is never an access token and the other way round
	key, _, err := newSecret(KeyPrefix)
	require.NoError(t, err)
	_, ok := parseSecret(key, AccessTokenPrefix)
	require.False(t, ok)
	at, _, err := newSecret(AccessTokenPrefix)
	require.NoError(t, err)
	_, ok = parseSecret(at, KeyPrefix)
	require.False(t, ok)

	id, err := randomString(clientRandomLen)
	require.NoError(t, err)
	require.True(t, validClientID(clientPrefix+id))
	require.False(t, validClientID(clientPrefix+id+"a"))
	require.False(t, validClientID("mcpc_"+strings.Repeat("!", 32)))
}

func TestWebRange(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	start, end, err := webRange("", "", now)
	require.NoError(t, err)
	require.Equal(t, now, end)
	require.Equal(t, now.Add(-7*24*time.Hour), start)

	start, end, err = webRange("2026-11-01", "2026-11-07", now)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), start)
	require.Equal(t, time.Date(2026, 11, 7, 23, 59, 59, 999_000_000, time.UTC), end)

	start, _, err = webRange("2026-11-01T09:00:00+02:00", "", now)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC), start)

	for _, c := range []struct{ from, to, want string }{
		{"yesterday", "", "ISO 8601"},
		{"2026-11-01T09:00:00", "", "ISO 8601"}, // no zone
		{"1999-12-31", "2000-01-02", "2000"},
		{"2026-11-08", "2026-11-07", "after from"},
		{"", "2026-11-20", "future"},
		{"2025-01-01", "2026-11-01", "366"},
	} {
		_, _, err := webRange(c.from, c.to, now)
		var te *toolError
		require.True(t, errors.As(err, &te), "%v", c)
		require.Equal(t, "invalid_input", te.Code)
		require.Contains(t, te.Message, c.want, "%v", c)
	}
	// exactly 366 whole days is allowed
	_, _, err = webRange("2025-11-14", "2026-11-14", now)
	require.NoError(t, err)

	v := rangeValues(time.UnixMilli(1000), time.UnixMilli(2000))
	require.Equal(t, "1000", v.Get("startAt"))
	require.Equal(t, "2000", v.Get("endAt"))
}

func TestCheckUnitAndTimezone(t *testing.T) {
	d := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, checkUnit("minute", d, d.Add(24*time.Hour)))
	require.Error(t, checkUnit("minute", d, d.Add(48*time.Hour)))
	require.NoError(t, checkUnit("hour", d, d.Add(31*24*time.Hour)))
	require.Error(t, checkUnit("hour", d, d.Add(60*24*time.Hour)))
	require.NoError(t, checkUnit("day", d, d.Add(366*24*time.Hour)))
	require.Error(t, checkUnit("week", d, d.Add(time.Hour)))

	tz, err := checkTimezone("")
	require.NoError(t, err)
	require.Equal(t, "UTC", tz)
	_, err = checkTimezone("Europe/Berlin")
	require.NoError(t, err)
	for _, bad := range []string{"Local", "Mars/Olympus", "../../etc/passwd", strings.Repeat("a", 65)} {
		_, err := checkTimezone(bad)
		require.Error(t, err, bad)
	}
}

func TestFilters(t *testing.T) {
	q := url.Values{}
	f := &webFilters{Path: "/pricing", Country: "DE", UTMSource: "c.news", Match: "any"}
	require.NoError(t, f.values(q, false))
	require.Equal(t, "/pricing", q.Get("path"))
	require.Equal(t, "DE", q.Get("country"))
	require.Equal(t, "c.news", q.Get("utmSource"))
	require.Equal(t, "any", q.Get("match"))
	// no parameter a tool did not name ever reaches umami
	require.Len(t, q, 4)

	require.Error(t, (&webFilters{Event: "signup"}).values(url.Values{}, false), "event filter on a page view tool")
	require.NoError(t, (&webFilters{Event: "signup"}).values(url.Values{}, true))
	require.Error(t, (&webFilters{Match: "some"}).values(url.Values{}, true))
	require.Error(t, (&webFilters{Path: "a\x00b"}).values(url.Values{}, true))
	require.Error(t, (&webFilters{Path: strings.Repeat("a", 501)}).values(url.Values{}, true))
	var none *webFilters
	require.NoError(t, none.values(url.Values{}, true))
}

func TestParseUUIDAndIDs(t *testing.T) {
	_, err := parseUUID("site_id", "0193A6F2-7C1E-7D40-9F55-2B1C2A5E8A10")
	require.Error(t, err, "upper case is not our form")
	_, err = parseUUID("site_id", "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10")
	require.NoError(t, err)

	a, b := "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10", "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a11"
	s, err := joinIDs("tenant_ids", []string{a, b}, 1, 5)
	require.NoError(t, err)
	require.Equal(t, a+","+b, s)
	_, err = joinIDs("tenant_ids", []string{a, a}, 1, 5)
	require.Error(t, err)
	_, err = joinIDs("tenant_ids", nil, 1, 5)
	require.Error(t, err)
	_, err = joinIDs("tenant_ids", []string{a, b, a + "x"}, 1, 5)
	require.Error(t, err)
}

func TestCheckRedirect(t *testing.T) {
	for _, ok := range []string{
		"https://claude.ai/api/mcp/auth_callback",
		"https://chatgpt.com/connector_platform_oauth_redirect",
		"http://localhost:33418/callback",
		"http://127.0.0.1:9999/cb",
		"http://[::1]:8000/cb",
		"cursor://anysphere.cursor-retrieval/oauth/user-veristar/callback",
		"com.example.app:/oauth2redirect",
	} {
		require.NoError(t, checkRedirect(ok), ok)
	}
	for _, bad := range []string{
		"http://example.org/cb", "https://example.org/cb#frag", "javascript:alert(1)", "data:text/html,x",
		"file:///etc/passwd", "/relative", "https:///nohost", "ws://localhost/cb", strings.Repeat("a", 2100),
	} {
		require.Error(t, checkRedirect(bad), bad)
	}
}

func TestSealedRequest(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	sk, err := sealKey(key)
	require.NoError(t, err)
	require.NotEqual(t, key, sk, "the seal key is derived, never the sign-in key itself")
	now := time.Now()
	s := &Server{sealKey: sk, now: func() time.Time { return now }}
	in := authRequest{ClientID: "mcpc_x", RedirectURI: "https://c.example/cb", State: "st", CodeChallenge: strings.Repeat("a", 43),
		Resource: "https://app.example.org/mcp", Expires: now.Add(requestTTL).Unix()}
	v, err := s.seal(in)
	require.NoError(t, err)
	out, err := s.unseal(v)
	require.NoError(t, err)
	require.Equal(t, in, out)

	// tampering, another key and expiry are all refused
	b := []byte(v)
	b[len(b)/2] ^= 1
	_, err = s.unseal(string(b))
	require.Error(t, err)
	other := make([]byte, 32)
	other[0] = 1
	ok, err := sealKey(other)
	require.NoError(t, err)
	_, err = (&Server{sealKey: ok, now: s.now}).unseal(v)
	require.Error(t, err)
	later := now.Add(requestTTL + time.Second)
	_, err = (&Server{sealKey: sk, now: func() time.Time { return later }}).unseal(v)
	require.Error(t, err)
	_, err = sealKey(key[:16])
	require.Error(t, err)
}

func TestLimits(t *testing.T) {
	l := newLimiter(1, 2)
	id := uuid.UUID{1}
	now := time.Now()
	ok, _ := l.take(id, now)
	require.True(t, ok)
	ok, _ = l.take(id, now)
	require.True(t, ok)
	ok, wait := l.take(id, now)
	require.False(t, ok)
	require.InDelta(t, time.Second, wait, float64(10*time.Millisecond))
	ok, _ = l.take(id, now.Add(time.Second))
	require.True(t, ok)

	f := &inflight{n: map[uuid.UUID]int{}}
	for range MaxConcurrentCalls {
		require.True(t, f.acquire(id))
	}
	require.False(t, f.acquire(id))
	f.release(id)
	require.True(t, f.acquire(id))
}

func TestRedirectOriginAndCallback(t *testing.T) {
	require.Equal(t, "https://claude.ai", redirectOrigin("https://claude.ai/api/mcp/auth_callback"))
	require.Equal(t, "cursor://anysphere.cursor-retrieval", redirectOrigin("cursor://anysphere.cursor-retrieval/oauth/x"))
	s := &Server{issuer: "https://app.example.org"}
	u, err := url.Parse(s.callbackURL("https://c.example/cb?x=1", url.Values{"code": {"abc"}}, "st"))
	require.NoError(t, err)
	require.Equal(t, "1", u.Query().Get("x"))
	require.Equal(t, "abc", u.Query().Get("code"))
	require.Equal(t, "st", u.Query().Get("state"))
	require.Equal(t, "https://app.example.org", u.Query().Get("iss"))
}

// a client that left redirect_uri out of the authorization request (it
// registered one uri) leaves it out of the token request too; a value that
// is given must be the code's exactly
func TestRedirectMatches(t *testing.T) {
	const issued = "http://127.0.0.1:9/cb"
	require.True(t, redirectMatches(url.Values{}, issued))
	require.True(t, redirectMatches(url.Values{"redirect_uri": {issued}}, issued))
	require.False(t, redirectMatches(url.Values{"redirect_uri": {""}}, issued))
	require.False(t, redirectMatches(url.Values{"redirect_uri": {"http://127.0.0.1:9/other"}}, issued))
	require.False(t, redirectMatches(url.Values{"redirect_uri": {issued, issued}}, issued))
}
