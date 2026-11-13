package prober

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"

	"github.com/justmert/stellar-analytics/internal/histo"
)

// probeService is the service.name of the probe metric. the probe tenant
// has no other service, so its api numbers are exactly the probe's requests.
const probeService = "prober"

// otlpRequest builds one delta histogram point of http.server.request.duration
// with count 1 on the canonical boundaries, the shape every tenant sdk
// sends. delta temporality makes each probe one request in api.metrics_minute
// without the first-point rule that holds back a new cumulative series.
func otlpRequest(start, end time.Time) ([]byte, error) {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", probeService)
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("github.com/justmert/stellar-analytics/internal/prober")
	m := sm.Metrics().AppendEmpty()
	m.SetName("http.server.request.duration")
	m.SetUnit("s")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
	dp := h.DataPoints().AppendEmpty()
	dp.SetStartTimestamp(pcommon.NewTimestampFromTime(start))
	dp.SetTimestamp(pcommon.NewTimestampFromTime(end))
	dp.Attributes().PutStr("http.request.method", "GET")
	dp.Attributes().PutStr("http.route", "/probe")
	dp.Attributes().PutInt("http.response.status_code", 200)
	dp.ExplicitBounds().FromRaw(histo.Bounds[:])
	counts := make([]uint64, histo.Buckets)
	// 0.004 s falls in the first bucket, (0, 0.005]
	counts[0] = 1
	dp.BucketCounts().FromRaw(counts)
	dp.SetCount(1)
	dp.SetSum(0.004)
	return pmetricotlp.NewExportRequestFromMetrics(md).MarshalProto()
}

// sendOTLP posts one metric to OTLP/HTTP. success is a 200, which the
// collector answers only after the jetstream exporter's acknowledged
// publish (docs/reference/collector.md).
func (p *Prober) sendOTLP(ctx context.Context) error {
	end := p.now()
	body, err := otlpRequest(end.Add(-p.cfg.Interval), end)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.OTLPURL.JoinPath("/v1/metrics").String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Authorization", "Bearer "+p.cfg.OTLPToken.Reveal())
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("otlp: %w", err)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("otlp: status %d %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	// a partial success names rejected points in a 200 body
	er := pmetricotlp.NewExportResponse()
	if err := er.UnmarshalProto(b); err == nil && er.PartialSuccess().RejectedDataPoints() > 0 {
		return fmt.Errorf("otlp: %d points rejected: %s", er.PartialSuccess().RejectedDataPoints(), er.PartialSuccess().ErrorMessage())
	}
	return nil
}
