package umami

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ProbeWebsiteID is a website id that is never created. the control plane
// gives websites random version 4 ids, so this fixed one stays unused.
const ProbeWebsiteID = "00000000-0000-4000-8000-000000000000"

// probeUserAgent is googlebot: should the probe website ever exist, umami's
// bot check answers {"beep":"boop"} and stores nothing.
const probeUserAgent = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

// websiteNotFound is the message of umami's 400 for an unknown website
// (src/app/api/send/route.ts, badRequest after fetchWebsite).
const websiteNotFound = "Website not found."

// ProbeCollection checks that /api/send can reach umami's database. the
// heartbeat answers {"ok":true} without touching the database, so it stays
// green while postgres is down and every send fails with 500. this probe
// posts an event for ProbeWebsiteID: umami looks the website up in the
// database and answers 400 "Website not found." when the lookup worked, and
// 500 when the database is unreachable. nothing is ever stored.
func (c *Client) ProbeCollection(ctx context.Context) error {
	hdr := http.Header{}
	hdr.Set("User-Agent", probeUserAgent)
	hdr.Set(EdgeClientIPHeader, EdgeClientIP)
	in := struct {
		Type    string `json:"type"`
		Payload Event  `json:"payload"`
	}{Type: "event", Payload: Event{Website: ProbeWebsiteID, Hostname: "probe.invalid", URL: "/", Timestamp: 1}}
	err := c.doHeaders(ctx, "collection probe", http.MethodPost, "/api/send", Token{}, hdr, in, nil)
	if err == nil {
		return fmt.Errorf("umami collection probe: website %s exists, expected 400", ProbeWebsiteID)
	}
	var api *APIError
	if errors.As(err, &api) && api.Status == http.StatusBadRequest && api.Message == websiteNotFound {
		return nil
	}
	return err
}

// Healthy runs the heartbeat and the collection probe. nil means umami as a
// whole can store events, so an event that still fails is failing on its
// own content.
func (c *Client) Healthy(ctx context.Context) error {
	if err := c.Heartbeat(ctx); err != nil {
		return err
	}
	return c.ProbeCollection(ctx)
}
