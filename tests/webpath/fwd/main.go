// Command fwd is a test-only tcp forwarder for the web path suite
// (tests/webpath). it runs in a container attached to docker networks whose
// subnets are db-ip ranges of real countries, and passes the browsers' tls
// connections through to the edge caddy on one of those networks. caddy then
// sees the forwarder's address on that network as its tcp peer, so the
// gateway resolves a real country through the production path (caddy's
// X-Edge-Client-Ip header, the db-ip lookup), without any test header.
//
//	fwd -listen :8443 -control :8080 -route DE=85.1.2.3:443 -route JP=... -initial DE
//
// POST /route?to=JP on the control port switches the target for new
// connections and closes every open one, so a pooled keep-alive connection
// cannot carry traffic from the previous address.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

type routes map[string]string

func (r routes) String() string { return fmt.Sprint(map[string]string(r)) }

func (r routes) Set(v string) error {
	name, addr, ok := strings.Cut(v, "=")
	if !ok || name == "" || addr == "" {
		return fmt.Errorf("route %q is not NAME=host:port", v)
	}
	r[name] = addr
	return nil
}

type forwarder struct {
	mu      sync.Mutex
	routes  routes
	current string
	open    map[net.Conn]struct{}
	total   map[string]int
}

func main() {
	rs := routes{}
	listen := flag.String("listen", ":8443", "tcp address the browsers' traffic arrives on")
	control := flag.String("control", ":8080", "http address of the route switch")
	initial := flag.String("initial", "", "route used until the first switch")
	flag.Var(rs, "route", "NAME=host:port, repeatable")
	flag.Parse()
	if _, ok := rs[*initial]; !ok {
		fmt.Fprintf(os.Stderr, "fwd: -initial %q is not one of the routes %v\n", *initial, rs)
		os.Exit(2)
	}
	f := &forwarder{routes: rs, current: *initial, open: map[net.Conn]struct{}{}, total: map[string]int{}}
	ctx := context.Background()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	go func() { log.Fatal(http.ListenAndServe(*control, f)) }() //nolint:gosec // test tool on a loopback-published port
	log.Printf("fwd: listening on %s, routes %v, initial %s", *listen, rs, *initial)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go f.handle(c)
	}
}

// ServeHTTP answers GET / with the current route and the connection counts,
// and POST /route?to=NAME with a switch.
func (f *forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		f.mu.Lock()
		_, _ = fmt.Fprintf(w, "{\"current\":%q,\"open\":%d,\"total\":%d}\n", f.current, len(f.open), f.total[f.current])
		f.mu.Unlock()
	case r.Method == http.MethodPost && r.URL.Path == "/route":
		// the name used from here on is the configured route key, not the
		// query text, so nothing the client sent reaches the log or the answer
		want := r.URL.Query().Get("to")
		to := ""
		f.mu.Lock()
		for name := range f.routes {
			if name == want {
				to = name
			}
		}
		if to == "" {
			f.mu.Unlock()
			http.Error(w, "unknown route", http.StatusBadRequest)
			return
		}
		f.current = to
		closed := len(f.open)
		for c := range f.open {
			_ = c.Close()
		}
		f.mu.Unlock()
		// json encoding, so the route name reaches the response only escaped
		log.Printf("fwd: route %q, closed %d open connections", to, closed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"current": to, "closed": closed})
	default:
		http.NotFound(w, r)
	}
}

func (f *forwarder) handle(in net.Conn) {
	f.mu.Lock()
	name := f.current
	target := f.routes[name]
	f.open[in] = struct{}{}
	f.total[name]++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.open, in)
		f.mu.Unlock()
		_ = in.Close()
	}()
	var d net.Dialer
	out, err := d.DialContext(context.Background(), "tcp", target)
	if err != nil {
		log.Printf("fwd: dial %s (%s): %v", target, name, err)
		return
	}
	defer out.Close()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		if _, err := io.Copy(dst, src); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("fwd: copy: %v", err)
		}
		done <- struct{}{}
	}
	go pipe(out, in)
	go pipe(in, out)
	// either side ending ends the pair; closing both unblocks the other copy
	<-done
}
