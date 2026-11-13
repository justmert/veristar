package operator

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// tamper decodes a cursor, lets edit change it and encodes it again, the
// way a key holder could edit the base64 json by hand. the hash has no
// secret, so the attacker also recomputes it with rehash.
func tamper(t *testing.T, cursor string, edit func(*machineCursor), rehash func(f, to string) string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	require.NoError(t, err)
	var c machineCursor
	require.NoError(t, json.Unmarshal(raw, &c))
	edit(&c)
	c.H = rehash(c.F, c.T)
	b, err := json.Marshal(c)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(b)
}

func requireBadCursor(t *testing.T, err error) {
	t.Helper()
	e, ok := httpx.AsError(err)
	require.True(t, ok, "want an httpx error, got %v", err)
	require.Equal(t, httpx.CodeInvalidCursor, e.Code)
}

//: the range a cursor carries is checked again on every
// page, so editing f or t cannot skip MaxRangeDays, maxAPIHours or the
// not-in-the-future rule of the first page.
func TestMachineCursorRangeIsCheckedOnEveryPage(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 30, 0, 0, time.UTC)
	key := machineKey{ID: uuid.New()}

	// day routes: a first page over 30 days, then the cursor widened
	dq := url.Values{"from": {"2026-10-15"}, "to": {"2026-11-13"}}
	p, err := parseMachinePage(dq, "machine-api-daily", key)
	require.NoError(t, err)
	rg, err := p.days(dq, now)
	require.NoError(t, err)
	cur := *p.next([]string{uuid.NewString(), "2026-10-20T00:00:00Z"}, rg.From.Format(time.DateOnly), rg.To.Format(time.DateOnly))

	page := func(q url.Values, c string) (machinePage, error) {
		q2 := url.Values{}
		for k, v := range q {
			q2[k] = v
		}
		q2.Set("cursor", c)
		return parseMachinePage(q2, "machine-api-daily", key)
	}
	// the untouched cursor still works on the next page, also after midnight
	p2, err := page(dq, cur)
	require.NoError(t, err)
	got, err := p2.days(dq, now.Add(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, rg, got)

	for name, edit := range map[string]func(*machineCursor){
		"ten years":      func(c *machineCursor) { c.F = "2016-01-01" },
		"future to":      func(c *machineCursor) { c.T = "2027-01-01" },
		"to before from": func(c *machineCursor) { c.F, c.T = "2026-11-13", "2026-10-15" },
	} {
		p3, err := page(dq, tamper(t, cur, edit, rehashFor(p)))
		if err == nil {
			_, err = p3.days(dq, now)
		}
		require.Error(t, err, name)
		requireBadCursor(t, err)
	}

	// hour routes: a first page of 24 hours, then the cursor widened past 744
	hq := url.Values{}
	hp, err := parseMachinePage(hq, "machine-api-hourly", key)
	require.NoError(t, err)
	hr, err := hp.hourSpan(hq, now, maxAPIHours)
	require.NoError(t, err)
	hcur := *hp.next([]string{uuid.NewString(), "2026-11-13T00:00:00Z"}, httpx.Time(hr.From), httpx.Time(hr.To))
	hpage := func(c string) (machinePage, error) {
		return parseMachinePage(url.Values{"cursor": {c}}, "machine-api-hourly", key)
	}
	hp2, err := hpage(hcur)
	require.NoError(t, err)
	got2, err := hp2.hourSpan(hq, now, maxAPIHours)
	require.NoError(t, err)
	require.Equal(t, hr, got2)
	for name, edit := range map[string]func(*machineCursor){
		"two years":   func(c *machineCursor) { c.F = "2024-11-13T00:00:00Z" },
		"future hour": func(c *machineCursor) { c.T = "2026-11-14T00:00:00Z" },
	} {
		hp3, err := hpage(tamper(t, hcur, edit, rehashFor(hp)))
		if err == nil {
			_, err = hp3.hourSpan(hq, now, maxAPIHours)
		}
		require.Error(t, err, name)
		requireBadCursor(t, err)
	}
}

// rehashFor recomputes a cursor hash as a key holder can: it is a plain
// sha-256 of values the key holder knows.
func rehashFor(p machinePage) func(f, to string) string {
	return func(f, to string) string { return p.hash(f, to) }
}
