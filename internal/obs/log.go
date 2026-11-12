// Package obs holds the logging, metrics and health endpoints every service shares.
package obs

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"unicode"
)

// RefusedKey is the attribute the guard adds to a record when it removed
// attributes, so a refused field shows up in the log rather than vanishing.
const RefusedKey = "log_guard_refused"

// forbidden words. a key is refused when its words, any single word, or any
// run of up to three adjacent words joined together is in this set. the
// privacy contract says client addresses, user agents and credentials never
// reach a log.
var forbidden = map[string]bool{
	"ip": true, "ips": true, "ipaddr": true, "ipaddress": true, "clientip": true, "remoteip": true,
	"remoteaddr": true, "xforwardedfor": true, "forwardedfor": true, "xrealip": true,
	"useragent": true, "ua": true,
	"authorization": true, "bearer": true,
	"cookie": true, "cookies": true, "setcookie": true,
	"token": true, "tokens": true,
	"password": true, "passwd": true, "pwd": true, "passphrase": true,
	"secret": true, "secrets": true, "apikey": true, "privatekey": true, "seed": true,
}

// words splits a key such as "clientIP", "x-forwarded-for" or "scf.token.id"
// into lowercase words.
func words(key string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(key)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) ||
			(unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			// a lower-to-upper step, or the last capital of an acronym, starts a word
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// Forbidden reports whether a log attribute or group key is refused by the guard.
func Forbidden(key string) bool {
	w := words(key)
	for i := range w {
		joined := ""
		for j := i; j < len(w) && j < i+3; j++ {
			joined += w[j]
			if forbidden[joined] {
				return true
			}
		}
	}
	return false
}

// guard wraps a handler and strips forbidden attributes before they are encoded.
type guard struct {
	next  slog.Handler
	group []string
	// refused holds keys removed by WithAttrs, reported again on every record
	refused []string
	// drop is set under a group whose own name is forbidden: nothing below it is kept
	drop bool
}

// NewLogger returns a json logger whose attribute keys pass through the guard.
func NewLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(NewGuard(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})))
}

// NewGuard wraps any handler with the forbidden key check.
func NewGuard(next slog.Handler) slog.Handler {
	return &guard{next: next}
}

func (g *guard) Enabled(ctx context.Context, l slog.Level) bool { return g.next.Enabled(ctx, l) }

func (g *guard) Handle(ctx context.Context, r slog.Record) error {
	refused := slices.Clone(g.refused)
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if g.drop {
			refused = append(refused, g.path(a.Key))
			return true
		}
		if kept, ok := filter(a, g.group, &refused); ok {
			out.AddAttrs(kept)
		}
		return true
	})
	if len(refused) > 0 {
		// inside a group this lands in the group too, which still makes it visible
		out.AddAttrs(slog.Any(RefusedKey, refused))
	}
	return g.next.Handle(ctx, out)
}

func (g *guard) WithAttrs(attrs []slog.Attr) slog.Handler {
	refused := slices.Clone(g.refused)
	kept := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if g.drop {
			refused = append(refused, g.path(a.Key))
			continue
		}
		if k, ok := filter(a, g.group, &refused); ok {
			kept = append(kept, k)
		}
	}
	next := g.next
	if len(kept) > 0 {
		next = next.WithAttrs(kept)
	}
	return &guard{next: next, group: g.group, refused: refused, drop: g.drop}
}

func (g *guard) WithGroup(name string) slog.Handler {
	if name == "" {
		return g
	}
	group := append(slices.Clone(g.group), name)
	if g.drop || Forbidden(name) {
		// the underlying handler never opens this group, so nothing under it is encoded
		return &guard{next: g.next, group: group, refused: g.refused, drop: true}
	}
	return &guard{next: g.next.WithGroup(name), group: group, refused: g.refused}
}

func (g *guard) path(key string) string {
	return strings.Join(append(slices.Clone(g.group), key), ".")
}

// filter returns the attribute with forbidden keys removed, recursing into groups.
func filter(a slog.Attr, path []string, refused *[]string) (slog.Attr, bool) {
	if Forbidden(a.Key) {
		*refused = append(*refused, strings.Join(append(slices.Clone(path), a.Key), "."))
		return slog.Attr{}, false
	}
	v := a.Value.Resolve()
	if v.Kind() != slog.KindGroup {
		return slog.Attr{Key: a.Key, Value: v}, true
	}
	sub := append(slices.Clone(path), a.Key)
	kept := make([]slog.Attr, 0, len(v.Group()))
	for _, c := range v.Group() {
		if k, ok := filter(c, sub, refused); ok {
			kept = append(kept, k)
		}
	}
	return slog.Attr{Key: a.Key, Value: slog.GroupValue(kept...)}, true
}
