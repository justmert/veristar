package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// dbipType is the database_type of the db-ip country lite file. any other
// file has a different record shape and is refused at open.
const dbipType = "DBIP-Country-Lite"

// dbipUnknown is the code db-ip uses for ranges it cannot place. it is not an
// iso 3166-1 country, so it is reported as unknown.
const dbipUnknown = "ZZ"

// Geo looks up the country of a client address in the local db-ip file
//. only the truncated network is looked up.
type Geo struct {
	path string

	// mu guards r. lookups hold it shared for the whole lookup and decode,
	// because results point into the reader's memory map, which Reload
	// unmaps when it closes the old reader.
	mu sync.RWMutex
	r  *maxminddb.Reader
}

// OpenGeo opens the db-ip country lite file at path and checks its type.
func OpenGeo(path string) (*Geo, error) {
	r, err := openDBIP(path)
	if err != nil {
		return nil, err
	}
	return &Geo{path: path, r: r}, nil
}

func openDBIP(path string) (*maxminddb.Reader, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("gateway: open geo database %s: %w", path, err)
	}
	md := r.Metadata
	if md.DatabaseType != dbipType || md.IPVersion != 6 {
		_ = r.Close()
		return nil, fmt.Errorf("gateway: geo database %s is %q with ip version %d, want %q with ip version 6",
			path, md.DatabaseType, md.IPVersion, dbipType)
	}
	return r, nil
}

// BuildTime is when db-ip built the loaded file.
func (g *Geo) BuildTime() time.Time {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.r.Metadata.BuildTime().UTC()
}

// Country returns the iso 3166-1 alpha-2 code for the address, or "" when the
// file has no country for it (private and reserved ranges, db-ip's ZZ). the
// address is cut to its /24 or /48 before the lookup.
func (g *Geo) Country(ip ClientIP) (string, error) {
	if !ip.IsValid() {
		return "", errors.New("gateway: geo lookup without a client address")
	}
	addr := ip.truncated().Addr()
	g.mu.RLock()
	defer g.mu.RUnlock()
	res := g.r.Lookup(addr)
	if err := res.Err(); err != nil {
		return "", fmt.Errorf("gateway: geo lookup: %w", err)
	}
	if !res.Found() {
		return "", nil
	}
	var code string
	if err := res.DecodePath(&code, "country", "iso_code"); err != nil {
		return "", fmt.Errorf("gateway: geo record: %w", err)
	}
	if code == "" || code == dbipUnknown {
		return "", nil
	}
	if !isISO2(code) {
		return "", fmt.Errorf("gateway: geo record holds country code %q, not two capital letters", code)
	}
	return code, nil
}

func isISO2(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// Reload opens the file at the same path again and swaps it in. the monthly
// update replaces the file and sends SIGHUP. if the new file cannot be
// opened the error is returned and the loaded file stays in use, so a bad
// download never leaves the gateway without a database.
func (g *Geo) Reload() error {
	r, err := openDBIP(g.path)
	if err != nil {
		return err
	}
	g.mu.Lock()
	old := g.r
	g.r = r
	g.mu.Unlock()
	return old.Close()
}

// ReloadOnSIGHUP reloads the file on every SIGHUP until ctx ends. failures
// are logged at error level, because the gateway then still serves last
// month's file.
func (g *Geo) ReloadOnSIGHUP(ctx context.Context, log *slog.Logger) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if err := g.Reload(); err != nil {
					log.Error("geo database reload failed, previous file still in use", "error", err.Error())
					continue
				}
				log.Info("geo database reloaded", "build_time", g.BuildTime().Format(time.RFC3339))
			}
		}
	}()
}

// Close releases the loaded file.
func (g *Geo) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.r.Close()
}
