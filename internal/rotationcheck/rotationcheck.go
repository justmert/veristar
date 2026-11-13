// Package rotationcheck proves a credential rotation (the design,
// rotate-all.yml): every previous credential is refused by the system that
// used to accept it, and every new one is accepted. a refusal counts only
// when the system said the credential was wrong; a connection that fails
// for any other reason (no route, a pg_hba rule, a timeout) is reported as
// inconclusive, so an unreachable server can never pass as "refused".
package rotationcheck

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nats-io/nats.go"

	"github.com/justmert/veristar/internal/config"
	"github.com/justmert/veristar/internal/umami"
)

// Pair is one credential before and after the rotation.
type Pair struct {
	Name string `json:"name"`
	User string `json:"user"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

// Postgres names a server and the logins to try against it. Database is
// the database each login connects to; "replication" sends the walsender
// startup parameter, which is how the replication role logs in.
type Postgres struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	SSLMode string `json:"sslmode"`
	// SSLRootCert is the internal ca; with it sslmode must be verify-full
	// (config.LoadPostgresTLS), so a check never logs in over plain text
	SSLRootCert string `json:"sslrootcert"`
	Logins      []struct {
		Pair
		Database    string `json:"database"`
		Replication bool   `json:"replication"`
	} `json:"logins"`
}

// NATS names the servers and the users to try.
type NATS struct {
	URLs  []string `json:"urls"`
	Users []Pair   `json:"users"`
	// CAFile is the internal ca; with it every attempt is tls from the
	// first byte, as the services connect (natsx.Options)
	CAFile string `json:"ca_file"`
}

// Umami names the instance, the admin passwords, and the previous and new
// user password keys; for a sample of console users the passwords both
// keys derive for today are tried.
type Umami struct {
	URL        string `json:"url"`
	Admin      Pair   `json:"admin"`
	UserKeyOld string `json:"user_key_old"`
	UserKeyNew string `json:"user_key_new"`
	// SampleUsers is how many console users (gh-<id>) are tried
	SampleUsers int `json:"sample_users"`
}

// Spec is the input of one check run; absent sections are not checked.
type Spec struct {
	Postgres *Postgres `json:"postgres"`
	NATS     *NATS     `json:"nats"`
	Umami    *Umami    `json:"umami"`
}

// Outcome of one login attempt.
type Outcome string

const (
	Accepted     Outcome = "accepted"
	Refused      Outcome = "refused"
	Inconclusive Outcome = "inconclusive"
)

// Result is the outcome for the previous and the new value of a credential.
type Result struct {
	Kind, Name string
	Old, New   Outcome
	Detail     string
}

// OK reports the state a completed rotation must reach.
func (r Result) OK() bool { return r.Old == Refused && r.New == Accepted }

func (r Result) String() string {
	s := fmt.Sprintf("%-8s %-28s previous %-12s new %-12s", r.Kind, r.Name, r.Old, r.New)
	if r.Detail != "" {
		s += " (" + r.Detail + ")"
	}
	return s
}

const attemptTimeout = 15 * time.Second

func pgAttempt(ctx context.Context, p *Postgres, user, pw, db string, repl bool) (Outcome, string) {
	cfg := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s connect_timeout=10",
		p.Host, p.Port, user, db, p.SSLMode)
	if p.SSLRootCert != "" {
		cfg += " sslrootcert=" + p.SSLRootCert
	}
	c, err := pgconn.ParseConfig(cfg)
	if err != nil {
		return Inconclusive, err.Error()
	}
	c.Password = pw
	if repl {
		c.RuntimeParams["replication"] = "true"
	}
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	conn, err := pgconn.ConnectConfig(ctx, c)
	if err == nil {
		_ = conn.Close(ctx)
		return Accepted, ""
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "28P01" {
		return Refused, ""
	}
	return Inconclusive, err.Error()
}

// natsTLS returns the tls options of the spec, the same as a service's
// (natsx.Options); none when no ca is named.
func natsTLS(n *NATS) ([]nats.Option, error) {
	if n.CAFile == "" {
		return nil, nil
	}
	cfg, err := config.LoadNATS(config.FromMap(map[string]string{
		"NATS_URL": strings.Join(n.URLs, ","), "NATS_REPLICAS": "1", "NATS_TLS_CA": n.CAFile,
	}))
	if err != nil {
		return nil, err
	}
	tc, err := cfg.TLS.ClientConfig()
	if err != nil {
		return nil, err
	}
	return []nats.Option{nats.Secure(tc), nats.TLSHandshakeFirst()}, nil
}

func natsAttempt(urls []string, tlsOpts []nats.Option, user, pw string) (Outcome, string) {
	opts := append([]nats.Option{nats.UserInfo(user, pw), nats.Timeout(attemptTimeout),
		nats.MaxReconnects(0), nats.NoReconnect(), nats.DontRandomize()}, tlsOpts...)
	nc, err := nats.Connect(strings.Join(urls, ","), opts...)
	if err == nil {
		nc.Close()
		return Accepted, ""
	}
	if errors.Is(err, nats.ErrAuthorization) || strings.Contains(strings.ToLower(err.Error()), "authorization violation") {
		return Refused, ""
	}
	return Inconclusive, err.Error()
}

func umamiAttempt(ctx context.Context, c *umami.Client, user, pw string) (Outcome, string) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	_, err := c.Login(ctx, user, pw)
	switch {
	case err == nil:
		return Accepted, ""
	case errors.Is(err, umami.ErrUnauthorized):
		return Refused, ""
	default:
		return Inconclusive, err.Error()
	}
}

func pair(kind, name string, attempt func(pw string) (Outcome, string), p Pair) Result {
	if p.Old == p.New {
		return Result{Kind: kind, Name: name, Old: Accepted, New: Accepted, Detail: "previous and new value are equal: not rotated"}
	}
	r := Result{Kind: kind, Name: name}
	var d1, d2 string
	r.Old, d1 = attempt(p.Old)
	r.New, d2 = attempt(p.New)
	r.Detail = strings.Trim(strings.Join([]string{d1, d2}, "; "), "; ")
	return r
}

// Run tries every credential of the spec once.
func Run(ctx context.Context, s Spec, now time.Time) ([]Result, error) {
	var out []Result
	if p := s.Postgres; p != nil {
		for _, l := range p.Logins {
			out = append(out, pair("postgres", l.Name, func(pw string) (Outcome, string) {
				return pgAttempt(ctx, p, l.User, pw, l.Database, l.Replication)
			}, l.Pair))
		}
	}
	if n := s.NATS; n != nil {
		tlsOpts, err := natsTLS(n)
		if err != nil {
			return nil, fmt.Errorf("nats tls: %w", err)
		}
		for _, u := range n.Users {
			out = append(out, pair("nats", u.Name, func(pw string) (Outcome, string) {
				return natsAttempt(n.URLs, tlsOpts, u.User, pw)
			}, u))
		}
	}
	if u := s.Umami; u != nil {
		base, err := url.Parse(u.URL)
		if err != nil {
			return nil, fmt.Errorf("umami url: %w", err)
		}
		c, err := umami.New(base, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, pair("umami", "admin", func(pw string) (Outcome, string) {
			return umamiAttempt(ctx, c, u.Admin.User, pw)
		}, u.Admin))
		if u.SampleUsers > 0 {
			oldKey, err1 := decodeKey(u.UserKeyOld)
			newKey, err2 := decodeKey(u.UserKeyNew)
			if err := errors.Join(err1, err2); err != nil {
				return nil, err
			}
			admin, err := umami.NewAdmin(c, umami.SeededUsername, u.Admin.New)
			if err != nil {
				return nil, err
			}
			users, err := admin.ListUsers(ctx)
			if err != nil {
				return nil, fmt.Errorf("umami list users: %w", err)
			}
			day := umami.EpochDay(now)
			tried := 0
			for _, x := range users {
				if !strings.HasPrefix(x.Username, "gh-") || tried == u.SampleUsers {
					continue
				}
				tried++
				id, name := x.ID, x.Username
				po, err1 := oldKey.Password(id, day)
				pn, err2 := newKey.Password(id, day)
				if err := errors.Join(err1, err2); err != nil {
					return nil, err
				}
				out = append(out, pair("umami", name, func(pw string) (Outcome, string) {
					return umamiAttempt(ctx, c, name, pw)
				}, Pair{Old: po, New: pn}))
			}
			if tried == 0 {
				return nil, errors.New("umami has no console user to try the user password keys on")
			}
		}
	}
	return out, nil
}

func decodeKey(b64 string) (umami.PasswordKey, error) {
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return umami.PasswordKey{}, fmt.Errorf("user password key: %w", err)
	}
	return umami.NewPasswordKey(k)
}
