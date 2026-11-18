package replayer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// sendLog records every /api/send that reaches umami through a sendProxy:
// which replayer node sent it, the event's path and when the send started
// and ended. tests use it to check the order in which each visitor's events
// reached umami and that no two nodes ever sent for one visitor at once.
type sendLog struct {
	mu      sync.Mutex
	entries []sendEntry
}

type sendEntry struct {
	node       string
	path       string
	status     int
	start, end time.Time
}

// newSendProxy serves a reverse proxy to target that records sends under
// node, and returns its url. it passes every other request through.
func newSendProxy(t *testing.T, log *sendLog, node string, target *url.URL) *url.URL {
	t.Helper()
	rp := httputil.NewSingleHostReverseProxy(target)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/send" {
			rp.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			Payload struct {
				URL string `json:"url"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(body, &req)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		rp.ServeHTTP(sw, r)
		end := time.Now()
		log.mu.Lock()
		log.entries = append(log.entries, sendEntry{node: node, path: req.Payload.URL, status: sw.status, start: start, end: end})
		log.mu.Unlock()
	})
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	u, err := url.Parse("http://" + l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return u
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// visitorPath is the path of event j of visitor i: /v<i>/<j>.
func visitorPath(i, j int) string { return fmt.Sprintf("/v%d/%d", i, j) }

func parseVisitorPath(p string) (int, int, bool) {
	parts := strings.Split(strings.TrimPrefix(p, "/v"), "/")
	if len(parts) != 2 || !strings.HasPrefix(p, "/v") {
		return 0, 0, false
	}
	i, err1 := strconv.Atoi(parts[0])
	j, err2 := strconv.Atoi(parts[1])
	return i, j, err1 == nil && err2 == nil
}

// orderReport is what checkOrder found.
type orderReport struct {
	// stored is the number of sends umami answered with 200
	stored int
	// repeats counts sends of an event umami had already stored
	repeats int
	// repeated describes each repeat: the event, the nodes and the times
	repeated []string
	// outOfOrder lists events stored after a later event of their visitor
	outOfOrder []string
	// overlaps lists sends of one visitor that ran at the same time
	overlaps []string
	// byNode counts stored sends per node
	byNode map[string]int
	// nodesPerVisitor is the most nodes that sent for one visitor
	nodesPerVisitor int
}

// checkOrder reads the log: per visitor, the stored events must come in
// path order (a repeat of the last one is a duplicate, not a reorder) and no
// two sends of the visitor may overlap in time, whichever node made them.
func (l *sendLog) checkOrder() orderReport {
	l.mu.Lock()
	entries := slices.Clone(l.entries)
	l.mu.Unlock()
	rep := orderReport{byNode: map[string]int{}}
	byVisitor := map[int][]sendEntry{}
	for _, e := range entries {
		i, _, ok := parseVisitorPath(e.path)
		if !ok {
			continue
		}
		byVisitor[i] = append(byVisitor[i], e)
	}
	for i, es := range byVisitor {
		slices.SortFunc(es, func(a, b sendEntry) int { return a.start.Compare(b.start) })
		for k := 1; k < len(es); k++ {
			if es[k].start.Before(es[k-1].end) {
				rep.overlaps = append(rep.overlaps, fmt.Sprintf("visitor %d: %s on %s overlaps %s on %s", i, es[k].path, es[k].node, es[k-1].path, es[k-1].node))
			}
		}
		last := -1
		nodes := map[string]bool{}
		seen := map[int]bool{}
		first := map[int]sendEntry{}
		for _, e := range es {
			if e.status != http.StatusOK {
				continue
			}
			_, j, _ := parseVisitorPath(e.path)
			rep.stored++
			rep.byNode[e.node]++
			nodes[e.node] = true
			if seen[j] {
				f := first[j]
				rep.repeated = append(rep.repeated, fmt.Sprintf("%s: %s %s-%s, then %s %s-%s", e.path,
					f.node, f.start.Format("15:04:05.000"), f.end.Format("15:04:05.000"),
					e.node, e.start.Format("15:04:05.000"), e.end.Format("15:04:05.000")))
			} else {
				first[j] = e
			}
			switch {
			case seen[j] && j == last:
				rep.repeats++
			case j < last:
				rep.outOfOrder = append(rep.outOfOrder, fmt.Sprintf("visitor %d: %d stored after %d", i, j, last))
				if seen[j] {
					rep.repeats++
				}
			default:
				last = j
			}
			seen[j] = true
		}
		rep.nodesPerVisitor = max(rep.nodesPerVisitor, len(nodes))
	}
	return rep
}
