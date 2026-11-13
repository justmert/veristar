package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"time"
)

// runClock measures how far each server's clock is from this machine's, and
// so from each other. lock fencing takes its epoch from nats server time and
// is safe while server clocks agree within the 15 s lock ttl
// (internal/natsx/fence.go), so the nats servers are read with nanosecond
// precision from /varz, and every node's caddy (port 80) and the core
// nodes' patroni through the http date header, which has one-second
// precision. each offset is corrected by half the round trip.
func runClock(ctx context.Context, args []string) (any, error) {
	fs := flag.NewFlagSet("clock", flag.ContinueOnError)
	invPath := fs.String("inv", "", "inventory json")
	maxSkew := fs.Duration("max-skew", time.Second, "largest allowed difference between two nats servers")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	inv, err := loadInventory(*invPath)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	natsOff := map[string]float64{}
	dateOff := map[string]float64{}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, e := range inv.group("edge") {
		t0 := time.Now()
		resp, err := httpGet(ctx, hc, "http://"+e.Private+":8222/varz")
		if err != nil {
			return nil, fmt.Errorf("varz of %s: %w", e.Name, err)
		}
		t1 := time.Now()
		var v struct {
			Now time.Time `json:"now"`
		}
		err = json.NewDecoder(resp.Body).Decode(&v)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		mid := t0.Add(t1.Sub(t0) / 2)
		off := v.Now.Sub(mid).Seconds()
		natsOff[e.Name] = off
		lo, hi = math.Min(lo, off), math.Max(hi, off)
	}
	for _, g := range []string{"edge", "core", "backup"} {
		for _, h := range inv.group(g) {
			targets := map[string]string{h.Name + " caddy": "http://" + h.Public + "/"}
			if g == "core" {
				targets[h.Name+" patroni"] = "http://" + h.Private + ":8008/health"
			}
			if g == "backup" {
				continue
			}
			for name, u := range targets {
				t0 := time.Now()
				resp, err := httpGet(ctx, hc, u)
				if err != nil {
					dateOff[name] = math.NaN()
					continue
				}
				t1 := time.Now()
				_ = resp.Body.Close()
				d, err := http.ParseTime(resp.Header.Get("Date"))
				if err != nil {
					continue
				}
				// the date header truncates to the second, so the server's
				// clock lies in [d, d+1s); the midpoint is the estimate
				mid := t0.Add(t1.Sub(t0) / 2)
				dateOff[name] = d.Add(500 * time.Millisecond).Sub(mid).Seconds()
			}
		}
	}
	spread := hi - lo
	res := map[string]any{
		"nats_offset_s":      natsOff,
		"nats_spread_s":      spread,
		"http_date_offset_s": dateOff,
		"max_allowed_s":      maxSkew.Seconds(),
		"fencing_budget_s":   15,
		"measured_at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if spread > maxSkew.Seconds() {
		res["alarm"] = true
		return res, fmt.Errorf("nats server clocks differ by %.6f s, more than %s", spread, maxSkew)
	}
	res["alarm"] = false
	return res, nil
}
