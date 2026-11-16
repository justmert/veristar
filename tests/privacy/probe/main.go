// Command probe is the web client of the system-wide privacy audit
// (tests/privacy). it runs in a container with a marker address on its own
// docker network and reaches the gateway only through the edge caddy, with
// the fetch metadata a browser's tracker fetch carries, so the gateway's
// checks pass and umami stores the events as a browser's. before the events
// it loads the tracker file twice as a page of the origin would, once with
// global privacy control, so the coverage counters run from the marker
// address too.
//
//	probe <base url> <site id> <origin> <user agent> <count>
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/justmert/veristar/tests/gateway/gwclient"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 5 {
		return fmt.Errorf("usage: probe <base url> <site id> <origin> <user agent> <count>")
	}
	base, site, origin, agent := args[0], args[1], args[2], args[3]
	n, err := strconv.Atoi(args[4])
	if err != nil || n < 1 {
		return fmt.Errorf("count %q is not a positive integer", args[4])
	}
	o, err := url.Parse(origin)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// what a browser sends for <script src> on a page of the origin
	for _, gpc := range []bool{false, true} {
		s := gwclient.New(base, true)
		s.Header.Set("User-Agent", agent)
		s.Header.Set("Referer", origin+"/audit/page")
		s.Header.Set("Sec-Fetch-Site", "cross-site")
		s.Header.Set("Sec-Fetch-Mode", "cors")
		s.Header.Set("Sec-Fetch-Dest", "script")
		if gpc {
			s.Header.Set("Sec-GPC", "1")
		}
		status, _, _, err := s.Get(ctx, "/v/1.3.0/sdk.js")
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("tracker file: status %d", status)
		}
	}
	c := gwclient.New(base, true)
	c.Header.Set("Origin", origin)
	c.Header.Set("User-Agent", agent)
	// what chromium sends for the tracker's cross-site fetch
	c.Header.Set("Sec-Fetch-Site", "cross-site")
	c.Header.Set("Sec-Fetch-Mode", "cors")
	c.Header.Set("Sec-Fetch-Dest", "empty")
	ch, err := c.Challenge(ctx, site)
	if err != nil {
		return err
	}
	p := gwclient.Solve(ch)
	for i := range n {
		status, err := c.PostEvent(ctx, gwclient.Pageview(site, o.Hostname(), "/audit/"+strconv.Itoa(i), &p), nil)
		if err != nil {
			return err
		}
		if status != 202 {
			return fmt.Errorf("event %d: status %d", i, status)
		}
	}
	fmt.Println("sent", n)
	return nil
}
