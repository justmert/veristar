package obs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

// CompositeHandler answers 200 when every check passes and 503 naming the
// first failing one. it backs the port-80 node health that the dns monitor
// reads: one answer for the whole node, so a node whose
// gateway, collector or local nats is unhealthy is taken out of dns.
// missing names configuration that the node needs before it may count as
// healthy; while it is non-empty the handler answers 503, so a node that was
// deployed without its composite inputs is never reported healthy.
func CompositeHandler(log *slog.Logger, missing []string, checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if len(missing) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "not configured: %v\n", missing)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		for _, c := range checks {
			if err := c.Fn(ctx); err != nil {
				log.Warn("composite health check failed", "check", c.Name, "error", err.Error())
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintf(w, "unhealthy: %s\n", c.Name)
				return
			}
		}
		_, _ = w.Write([]byte("healthy\n"))
	})
}

// HTTPCheck is a check that passes when a GET of u answers 200. it is used
// for neighbours on the same node that expose their own health endpoint
// (the collector's health_check extension, the nats monitoring /healthz).
func HTTPCheck(name string, u *url.URL, client *http.Client) Check {
	return Check{Name: name, Fn: func(ctx context.Context) error {
		if u == nil {
			return errors.New("no url")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s answered %d", u.Redacted(), resp.StatusCode)
		}
		return nil
	}}
}
