package umamiread

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/umami"
)

type fakeSessions struct {
	mu     sync.Mutex
	logins int
	site   Site
	err    error
}

func (f *fakeSessions) SiteSession(_ context.Context, _, siteID uuid.UUID) (Site, umami.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	if f.err != nil {
		return Site{}, umami.Session{}, f.err
	}
	s := f.site
	if s.ID != siteID {
		// the database answers for the site that was asked
		s.ID = siteID
	}
	return s, umami.Session{}, nil
}

type fakeReader struct {
	mu      sync.Mutex
	calls   []string
	answers []error
}

func (f *fakeReader) ReadWebsite(_ context.Context, _ umami.Token, websiteID, sub string, _ url.Values, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, websiteID+sub)
	if len(f.answers) == 0 {
		return nil
	}
	err := f.answers[0]
	f.answers = f.answers[1:]
	return err
}

func TestReader(t *testing.T) {
	tenant, site, quarantine := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	newEnv := func() (*fakeSessions, *fakeReader, *Reader) {
		s := &fakeSessions{site: Site{ID: site, TenantID: tenant, Status: "active", QuarantineWebsiteID: uuid.NullUUID{UUID: quarantine, Valid: true}}}
		r := &fakeReader{}
		rd, err := NewReader(s, r, clock)
		require.NoError(t, err)
		return s, r, rd
	}
	user := uuid.New()
	primary := Website{Tenant: tenant, Site: site, ID: site.String()}

	t.Run("a login is reused for both websites of the site", func(t *testing.T) {
		s, r, rd := newEnv()
		require.NoError(t, rd.Read(t.Context(), user, primary, "/stats", nil, nil))
		require.NoError(t, rd.Read(t.Context(), user, Website{Tenant: tenant, Site: site, ID: quarantine.String()}, "/stats", nil, nil))
		require.Equal(t, 1, s.logins)
		require.Equal(t, []string{site.String() + "/stats", quarantine.String() + "/stats"}, r.calls)
	})
	t.Run("a 401 logs in once more", func(t *testing.T) {
		s, r, rd := newEnv()
		r.answers = []error{&umami.APIError{Status: 401}}
		require.NoError(t, rd.Read(t.Context(), user, primary, "/stats", nil, nil))
		require.Equal(t, 2, s.logins)
		r.answers = []error{&umami.APIError{Status: 401}, &umami.APIError{Status: 401}}
		err := rd.Read(t.Context(), user, primary, "/stats", nil, nil)
		var ue *UmamiError
		require.ErrorAs(t, err, &ue)
		require.Equal(t, 401, ue.Status())
	})
	t.Run("a session of another tenant never reads", func(t *testing.T) {
		s, r, rd := newEnv()
		s.site.TenantID = uuid.New()
		err := rd.Read(t.Context(), user, primary, "/stats", nil, nil)
		require.Error(t, err)
		_, isFailure := FailureCode(err)
		require.False(t, isFailure, "an internal error, not a failure the caller shows")
		require.Empty(t, r.calls)
	})
	t.Run("a website that is not the site's never reaches umami", func(t *testing.T) {
		s, r, rd := newEnv()
		err := rd.Read(t.Context(), user, Website{Tenant: tenant, Site: site, ID: uuid.NewString()}, "/stats", nil, nil)
		require.Error(t, err)
		require.Empty(t, r.calls)
		require.Equal(t, 1, s.logins)
		// a cached site is read again once before refusing, because it may
		// predate its quarantine website
		require.NoError(t, rd.Read(t.Context(), user, primary, "/stats", nil, nil))
		require.Error(t, rd.Read(t.Context(), user, Website{Tenant: tenant, Site: site, ID: uuid.NewString()}, "/stats", nil, nil))
		require.Equal(t, 2, s.logins)
		require.Len(t, r.calls, 1)
	})
	t.Run("failures pass through with their code", func(t *testing.T) {
		s, _, rd := newEnv()
		s.err = fail(CodeProvisioningPending, nil)
		err := rd.Read(t.Context(), user, primary, "/stats", nil, nil)
		code, ok := FailureCode(err)
		require.True(t, ok)
		require.Equal(t, CodeProvisioningPending, code)
	})
	t.Run("a transport error has no status", func(t *testing.T) {
		_, r, rd := newEnv()
		r.answers = []error{errors.New("dial tcp: refused")}
		var ue *UmamiError
		require.ErrorAs(t, rd.Read(t.Context(), user, primary, "/stats", nil, nil), &ue)
		require.Zero(t, ue.Status())
	})
	t.Run("logins expire", func(t *testing.T) {
		s, _, rd := newEnv()
		require.NoError(t, rd.Read(t.Context(), user, primary, "/stats", nil, nil))
		now = now.Add(sessionTTL)
		require.NoError(t, rd.Read(t.Context(), user, primary, "/stats", nil, nil))
		require.Equal(t, 2, s.logins)
	})
}

func TestSessionsCheck(t *testing.T) {
	require.Error(t, (&Sessions{}).Check())
}
