package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

// runProbe reads the dashboard on every core node at an interval, as a
// browser with both core addresses would, and reports the longest stretch in
// which no node gave an answer from the database (the dashboard gap), plus
// the gap of each node on its own. the endpoint is the tenant api overview,
// which queries the platform database on every call.
func runProbe(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	statePath := fs.String("state", "", "state from setup")
	dur := fs.Duration("duration", 3*time.Minute, "how long to probe")
	every := fs.Duration("every", 500*time.Millisecond, "probe interval")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	var st state
	if err := readJSON(*statePath, &st); err != nil {
		return nil, err
	}
	cores := inv.group("core")
	clients := map[string]*http.Client{}
	for _, c := range cores {
		clients[c.Name] = &http.Client{Transport: pinnedTransport(c.Public, 2*time.Second), Timeout: 3 * time.Second}
	}
	start := time.Now()
	lastOK := map[string]time.Time{"any": start}
	worst := map[string]float64{"any": 0}
	fails := map[string]int{}
	for _, c := range cores {
		lastOK[c.Name] = start
		worst[c.Name] = 0
	}
	var worstAnyAt time.Time
	tick := time.NewTicker(*every)
	defer tick.Stop()
	for now := range tick.C {
		if now.Sub(start) > *dur || ctx.Err() != nil {
			break
		}
		to := time.Now().UTC()
		q := url.Values{"from": {to.Add(-time.Hour).Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
		anyOK := false
		for _, c := range cores {
			ok := probeOnce(ctx, clients[c.Name], st, "https://"+st.AppHost+"/api/tenants/"+st.DashProbe.Tenant+"/api-metrics/overview?"+q.Encode())
			if ok {
				lastOK[c.Name] = time.Now()
				anyOK = true
			} else {
				fails[c.Name]++
				worst[c.Name] = math.Max(worst[c.Name], time.Since(lastOK[c.Name]).Seconds())
			}
		}
		if anyOK {
			lastOK["any"] = time.Now()
		} else if g := time.Since(lastOK["any"]).Seconds(); g > worst["any"] {
			worst["any"] = g
			worstAnyAt = lastOK["any"]
		}
	}
	res := map[string]any{"longest_gap_s": worst, "failed_probes": fails, "interval_s": every.Seconds()}
	if !worstAnyAt.IsZero() {
		res["gap_started"] = worstAnyAt.UTC().Format(time.RFC3339Nano)
	}
	return res, nil
}

func probeOnce(ctx context.Context, hc *http.Client, st state, u string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	for _, c := range st.Cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var v map[string]any
	return resp.StatusCode == http.StatusOK && json.Unmarshal(b, &v) == nil
}

// runLag reads how many messages the work-queue streams still hold. a
// work-queue stream deletes a message once its consumer acknowledged it, so
// the message count is the backlog; consumer counters read from a server
// that does not lead the consumer can be stale. with -until-zero it waits
// until all of them are empty.
func runLag(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("lag", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	wait := fs.Duration("until-zero", 0, "wait up to this long for zero")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	for {
		lag, err := natsLag(ctx, inv)
		// EDGE_STATS is folded by jobs on its own schedule, so it is reported
		// but not part of the event backlog
		total := lag["WEB_EVENTS"] + lag["API_METRICS"]
		if err == nil && (*wait == 0 || total == 0) {
			return map[string]any{"consumers": lag, "total": total, "waited_s": time.Since(start).Seconds()}, nil
		}
		if time.Since(start) > *wait || ctx.Err() != nil {
			return map[string]any{"consumers": lag, "total": total, "waited_s": time.Since(start).Seconds()}, fmt.Errorf("lag not zero: %d (%w)", total, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func natsLag(ctx context.Context, inv inventory) (map[string]int64, error) {
	tr, err := privateTransport()
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	var lastErr error
	for _, e := range inv.group("edge") {
		// nats monitoring through the relay's tls listener (roles/observability)
		resp, err := httpGet(ctx, hc, "https://"+e.Private+":8222/jsz?accounts=true&streams=true")
		if err != nil {
			lastErr = err
			continue
		}
		var doc struct {
			Accounts []struct {
				Streams []struct {
					Name  string `json:"name"`
					State struct {
						Messages int64 `json:"messages"`
					} `json:"state"`
				} `json:"stream_detail"`
			} `json:"account_details"`
		}
		err = json.NewDecoder(resp.Body).Decode(&doc)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		out := map[string]int64{}
		for _, a := range doc.Accounts {
			for _, s := range a.Streams {
				switch s.Name {
				case "WEB_EVENTS", "API_METRICS", "EDGE_STATS":
					out[s.Name] = s.State.Messages
				}
			}
		}
		if len(out) == 3 {
			return out, nil
		}
		lastErr = fmt.Errorf("%s reports %d of the 3 work-queue streams", e.Name, len(out))
	}
	return nil, lastErr
}
