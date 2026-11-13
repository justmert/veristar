// Command probe is the web client of the this stage privacy audit
// (tests/privacy). it runs in a container with a marker address on its own
// docker network and reaches the gateway only through the edge caddy, with
// the fetch metadata a browser's tracker fetch carries, so the gateway's
// checks pass and umami stores the events as a browser's.
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
