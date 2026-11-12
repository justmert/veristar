//go:build otelapps

package otelapps

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
)

// the structs below decode the otlp json lines the stock file exporter
// writes. only the fields the checks read are declared, and plain
// encoding/json is used so this package adds nothing to the root go.mod.

type anyValue struct {
	StringValue *string `json:"stringValue"`
	IntValue    *string `json:"intValue"`
}

func (v anyValue) String() string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return *v.IntValue
	}
	return ""
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type attrs []keyValue

func (a attrs) get(keys ...string) string {
	for _, k := range keys {
		for _, kv := range a {
			if kv.Key == k {
				return kv.Value.String()
			}
		}
	}
	return ""
}

type resource struct {
	Attributes attrs `json:"attributes"`
}

type dataPoint struct {
	Attributes     attrs           `json:"attributes"`
	StartTimeNano  string          `json:"startTimeUnixNano"`
	TimeNano       string          `json:"timeUnixNano"`
	Count          string          `json:"count"`
	ExplicitBounds []float64       `json:"explicitBounds"`
	Scale          *int            `json:"scale"`
	Positive       json.RawMessage `json:"positive"`
}

type histogram struct {
	DataPoints  []dataPoint `json:"dataPoints"`
	Temporality int         `json:"aggregationTemporality"`
}

type metric struct {
	Name                 string     `json:"name"`
	Unit                 string     `json:"unit"`
	Histogram            *histogram `json:"histogram"`
	ExponentialHistogram *histogram `json:"exponentialHistogram"`
}

type metricsLine struct {
	ResourceMetrics []struct {
		Resource     resource `json:"resource"`
		ScopeMetrics []struct {
			Metrics []metric `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

type event struct {
	Name string `json:"name"`
}

type span struct {
	Name       string  `json:"name"`
	Kind       int     `json:"kind"`
	Attributes attrs   `json:"attributes"`
	Events     []event `json:"events"`
}

type tracesLine struct {
	ResourceSpans []struct {
		Resource   resource `json:"resource"`
		ScopeSpans []struct {
			Spans []span `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

// otlp span kinds from the proto enum
const (
	spanKindInternal = 1
	spanKindServer   = 2
)

// otlp aggregation temporality from the proto enum
const (
	temporalityDelta      = 1
	temporalityCumulative = 2
)

// readLines decodes every json line of path into fn. a missing file means
// the collector never received that signal, which is an empty result.
func readLines(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	n := 0
	for sc.Scan() {
		n++
		if len(sc.Bytes()) == 0 {
			continue
		}
		if err := fn(sc.Bytes()); err != nil {
			return fmt.Errorf("%s line %d: %w", path, n, err)
		}
	}
	return sc.Err()
}

// requestKey identifies one counted bucket of traffic.
type requestKey struct {
	Method, Route string
	Status        int
}

func (k requestKey) String() string { return fmt.Sprintf("%s %s %d", k.Method, k.Route, k.Status) }

// durationSummary is what one backend received for the request duration
// metric of one service.
type durationSummary struct {
	Names        map[string]bool
	Units        map[string]bool
	Temporality  map[int]bool
	Exponential  bool
	Explicit     bool
	Counts       map[requestKey]int64
	DataPoints   int
	Exports      map[string]bool // distinct data point timestamps, one per collection
	OtherMetrics map[string]bool
}

// both the stable and the legacy duration metric names count as requests
var durationNames = map[string]bool{"http.server.request.duration": true, "http.server.duration": true}

// summarizeMetrics totals request counts per method, route and status. delta
// points are summed; cumulative points of one stream (same attributes and
// start time) are reduced to their latest value before summing streams.
func summarizeMetrics(path, service string) (durationSummary, error) {
	s := durationSummary{Names: map[string]bool{}, Units: map[string]bool{}, Temporality: map[int]bool{},
		Counts: map[requestKey]int64{}, Exports: map[string]bool{}, OtherMetrics: map[string]bool{}}
	type stream struct {
		key   requestKey
		attrs string
		start string
	}
	latest := map[stream][2]int64{} // time, count
	err := readLines(path, func(b []byte) error {
		var l metricsLine
		if err := json.Unmarshal(b, &l); err != nil {
			return err
		}
		for _, rm := range l.ResourceMetrics {
			if rm.Resource.Attributes.get("service.name") != service {
				continue
			}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if !durationNames[m.Name] {
						s.OtherMetrics[m.Name] = true
						continue
					}
					h := m.Histogram
					if h != nil {
						s.Explicit = true
					} else if m.ExponentialHistogram != nil {
						h = m.ExponentialHistogram
						s.Exponential = true
					} else {
						return fmt.Errorf("metric %s is not a histogram", m.Name)
					}
					s.Names[m.Name] = true
					s.Units[m.Unit] = true
					s.Temporality[h.Temporality] = true
					for _, dp := range h.DataPoints {
						s.DataPoints++
						s.Exports[dp.TimeNano] = true
						status, err := strconv.Atoi(dp.Attributes.get("http.response.status_code", "http.status_code"))
						if err != nil {
							return fmt.Errorf("data point without a status code: %v", dp.Attributes)
						}
						// the legacy python metric has no http.route; its instrumentation
						// records the route template as http.target instead
						k := requestKey{dp.Attributes.get("http.request.method", "http.method"),
							dp.Attributes.get("http.route", "http.target"), status}
						n, err := strconv.ParseInt(dp.Count, 10, 64)
						if err != nil {
							return err
						}
						if h.Temporality == temporalityDelta {
							s.Counts[k] += n
							continue
						}
						ab, _ := json.Marshal(dp.Attributes)
						st := stream{k, string(ab), dp.StartTimeNano}
						t, _ := strconv.ParseInt(dp.TimeNano, 10, 64)
						if prev, ok := latest[st]; !ok || t >= prev[0] {
							latest[st] = [2]int64{t, n}
						}
					}
				}
			}
		}
		return nil
	})
	for st, v := range latest {
		s.Counts[st.key] += v[1]
	}
	return s, err
}

// traceSummary is what one backend received as spans of one service.
type traceSummary struct {
	Server         map[requestKey]int64
	MarkerSpans    int
	MarkerEvents   int
	ServerNoRoute  int
	OtherKindSpans int
}

func summarizeTraces(path, service string) (traceSummary, error) {
	s := traceSummary{Server: map[requestKey]int64{}}
	err := readLines(path, func(b []byte) error {
		var l tracesLine
		if err := json.Unmarshal(b, &l); err != nil {
			return err
		}
		for _, rs := range l.ResourceSpans {
			if rs.Resource.Attributes.get("service.name") != service {
				continue
			}
			for _, ss := range rs.ScopeSpans {
				for _, sp := range ss.Spans {
					for _, e := range sp.Events {
						if e.Name == "SA_MARKER_span_event" {
							s.MarkerEvents++
						}
					}
					if sp.Name == "SA_MARKER_internal_span" && sp.Kind == spanKindInternal {
						s.MarkerSpans++
					}
					if sp.Kind != spanKindServer {
						s.OtherKindSpans++
						continue
					}
					status, err := strconv.Atoi(sp.Attributes.get("http.response.status_code", "http.status_code"))
					if err != nil {
						return fmt.Errorf("server span %q without a status code", sp.Name)
					}
					route := sp.Attributes.get("http.route")
					if route == "" {
						s.ServerNoRoute++
					}
					s.Server[requestKey{sp.Attributes.get("http.request.method", "http.method"), route, status}]++
				}
			}
		}
		return nil
	})
	return s, err
}
