package gateway_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/registry"
	"github.com/justmert/veristar/tests/gateway/gwclient"
)

// utm collection is read from the site's registry entry on every event: a
// site that never turned it on loses utm_* and ref, one that did keeps them,
// and turning it off again takes effect without a restart. every other
// query parameter is dropped either way.
func TestUTMCollectionFollowsTheRegistry(t *testing.T) {
	e := startNode(t, nil)
	p, err := registry.NewPublisher(t.Context(), e.js)
	require.NoError(t, err)
	s := nodeSite{ID: uuid.NewString(), Quarantine: uuid.NewString(), Origin: "https://campaign.example", Host: "campaign.example"}
	put := func(collect bool) {
		require.NoError(t, p.PutSite(t.Context(), registry.Site{ID: s.ID, TenantID: uuid.NewString(), Status: "active",
			Origins: []string{s.Origin}, QuarantineWebsiteID: s.Quarantine, CollectUTM: collect}))
	}
	const path = "/landing?utm_source=news&utm_campaign=launch&ref=friend&q=private"

	put(false)
	c := e.client(t, s, "198.51.100.20")
	pr := proof(t, c, s.ID)
	require.Equal(t, http.StatusAccepted, post(t, c, gwclient.Pageview(s.ID, s.Host, path, pr), nil))
	require.Equal(t, "/landing", e.lastQueued(t).URL)

	put(true)
	require.Eventually(t, func() bool {
		if post(t, c, gwclient.Pageview(s.ID, s.Host, path, proof(t, c, s.ID)), nil) != http.StatusAccepted {
			return false
		}
		return e.lastQueued(t).URL == "/landing?utm_source=news&utm_campaign=launch&ref=friend"
	}, 30*time.Second, 200*time.Millisecond, "the setting reaches the node")

	put(false)
	require.Eventually(t, func() bool {
		if post(t, c, gwclient.Pageview(s.ID, s.Host, path, proof(t, c, s.ID)), nil) != http.StatusAccepted {
			return false
		}
		return e.lastQueued(t).URL == "/landing"
	}, 30*time.Second, 200*time.Millisecond, "turning it off reaches the node")
}
