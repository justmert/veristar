package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"

	"github.com/justmert/stellar-analytics/internal/histo"
)

// otlpMany sends n single-point requests, 40 per second: the collector
// limits a tenant to 50 requests per second, and a refused request is not
// queued. each accepted request is one API_METRICS message, which is what
// the metrics writer lag alert counts.
func otlpMany(c *http.Client, token string, n int) error {
	if token == "" || n < 1 {
		return errors.New("-token and -n are required")
	}
	var ok, bad atomic.Int64
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range jobs {
				md := pmetric.NewMetrics()
				rm := md.ResourceMetrics().AppendEmpty()
				rm.Resource().Attributes().PutStr("service.name", "prober")
				m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
				m.SetName("http.server.request.duration")
				m.SetUnit("s")
				h := m.SetEmptyHistogram()
				h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
				dp := h.DataPoints().AppendEmpty()
				now := time.Now()
				dp.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(-time.Second)))
				dp.SetTimestamp(pcommon.NewTimestampFromTime(now))
				dp.Attributes().PutStr("http.request.method", "GET")
				dp.Attributes().PutStr("http.route", "/probe")
				dp.Attributes().PutInt("http.response.status_code", 200)
				dp.ExplicitBounds().FromRaw(histo.Bounds[:])
				counts := make([]uint64, histo.Buckets)
				counts[0] = 1
				dp.BucketCounts().FromRaw(counts)
				dp.SetCount(1)
				dp.SetSum(0.001)
				b, _ := pmetricotlp.NewExportRequestFromMetrics(md).MarshalProto()
				resp, _, err := do(c, http.MethodPost, "https://otlp.p17.test/v1/metrics", bytes.NewReader(b), map[string]string{
					"Content-Type": "application/x-protobuf", "Authorization": "Bearer " + token,
				})
				if err == nil && resp.StatusCode == http.StatusOK {
					ok.Add(1)
				} else {
					bad.Add(1)
				}
			}
		})
	}
	tick := time.NewTicker(time.Second / 40)
	for i := range n {
		<-tick.C
		jobs <- i
	}
	tick.Stop()
	close(jobs)
	wg.Wait()
	fmt.Printf("otlp requests: %d ok, %d not ok\n", ok.Load(), bad.Load())
	return nil
}

// events sends n pageviews through the tracker protocol. the gateway
// limits each address prefix to 20 per second with a burst of 60, so the
// loop keeps under that.
func events(c *http.Client, site, origin string, n int) error {
	if site == "" || origin == "" || n < 1 {
		return errors.New("-site, -origin and -n are required")
	}
	o, err := url.Parse(origin)
	if err != nil {
		return err
	}
	h := map[string]string{
		"Origin": origin, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty",
		"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:146.0) Gecko/20100101 Firefox/146.0",
	}
	tick := time.NewTicker(time.Second / 15)
	defer tick.Stop()
	ok, bad := 0, 0
	for range n {
		<-tick.C
		resp, b, err := do(c, http.MethodGet, "https://in.p17.test/v1/c?w="+url.QueryEscape(site), nil, h)
		if err != nil || resp.StatusCode != http.StatusOK {
			bad++
			continue
		}
		var ch struct {
			W    string `json:"w"`
			IAT  int64  `json:"iat"`
			Salt string `json:"salt"`
			Bits int    `json:"bits"`
			Tag  string `json:"tag"`
		}
		if json.Unmarshal(b, &ch) != nil {
			bad++
			continue
		}
		prefix := ch.W + "|" + strconv.FormatInt(ch.IAT, 10) + "|" + ch.Salt + "|"
		var nonce uint64
		for ; ; nonce++ {
			d := sha256.Sum256([]byte(prefix + strconv.FormatUint(nonce, 10)))
			z := 0
			for _, x := range d {
				if x != 0 {
					z += bits.LeadingZeros8(x)
					break
				}
				z += 8
			}
			if z >= ch.Bits {
				break
			}
		}
		ev, _ := json.Marshal(map[string]any{"v": 1, "w": site, "t": "pv", "h": o.Hostname(), "u": "/drill", "r": "", "ti": "drill",
			"s": "1920x1080", "l": "en-US", "ts": time.Now().UnixMilli(),
			"p": map[string]any{"iat": ch.IAT, "salt": ch.Salt, "bits": ch.Bits, "tag": ch.Tag, "nonce": nonce}})
		h2 := map[string]string{"Content-Type": "text/plain;charset=UTF-8"}
		for k, v := range h {
			h2[k] = v
		}
		resp, _, err = do(c, http.MethodPost, "https://in.p17.test/v1/e", bytes.NewReader(ev), h2)
		if err == nil && resp.StatusCode == http.StatusAccepted {
			ok++
		} else {
			bad++
		}
	}
	fmt.Printf("web events: %d accepted, %d not\n", ok, bad)
	return nil
}

func dlq(natsURL, action string) error {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch action {
	case "publish":
		_, err = js.Publish(ctx, "dlq.web.drill", []byte(`{"drill":"this stage DLQNotEmpty"}`))
	case "purge":
		var s jetstream.Stream
		if s, err = js.Stream(ctx, "WEB_DLQ"); err == nil {
			err = s.Purge(ctx)
		}
	default:
		err = errors.New("-action must be publish or purge")
	}
	if err == nil {
		fmt.Println("dlq", action, "done")
	}
	return err
}
