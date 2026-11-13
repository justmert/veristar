// Package db connects services to the platform database with one pool per
// database role, wraps every unit of work in a transaction of the
// right role, and applies migrations and role passwords for platformctl.
package db

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/justmert/veristar/internal/config"
)

// Role is a postgresql role an application connects as.
type Role string

// application roles from the design. none of them has bypassrls.
const (
	RoleCPSystem      Role = "cp_system"
	RoleCPTenant      Role = "cp_tenant"
	RoleCPOperator    Role = "cp_operator"
	RoleMetricsWriter Role = "metrics_writer"
	RoleJobs          Role = "jobs"
)

// OwnerRole owns every object in the platform database. it never logs in;
// migrations run as the admin login with set role to it.
const OwnerRole = "platform_owner"

// AppRoles lists every application role, in a fixed order.
var AppRoles = []Role{RoleCPSystem, RoleCPTenant, RoleCPOperator, RoleMetricsWriter, RoleJobs}

func (r Role) valid() bool {
	for _, a := range AppRoles {
		if r == a {
			return true
		}
	}
	return false
}

// PasswordKey is the environment key that holds the role's login password,
// for example PLATFORM_DB_CP_TENANT_PASSWORD.
func (r Role) PasswordKey() string {
	return "PLATFORM_DB_" + strings.ToUpper(string(r)) + "_PASSWORD"
}

// role passwords are generated, never typed, so a long minimum costs nothing
const minRolePassword = 32

// Server says where the platform database is.
type Server struct {
	Host     string
	Port     int
	Database string
	// SSLMode is a libpq sslmode. it is required rather than defaulted so that
	// production never runs without tls by omission.
	SSLMode string
	// TLS names the internal ca and an optional client certificate
	// (PLATFORM_DB_SSLROOTCERT, PLATFORM_DB_SSLCERT, PLATFORM_DB_SSLKEY).
	// with a ca, SSLMode is verify-full, so a connection is encrypted and
	// the server's name checked, or it fails; it never continues in clear.
	TLS *config.TLSFiles
}

// Login is a user name and password for the platform database.
type Login struct {
	User     string
	Password config.Secret
}

var sslModes = map[string]bool{"disable": true, "require": true, "verify-ca": true, "verify-full": true}

// loadServer reads PLATFORM_DB_HOST, PLATFORM_DB_PORT, PLATFORM_DB_NAME,
// PLATFORM_DB_SSLMODE and the optional PLATFORM_DB_SSLROOTCERT,
// PLATFORM_DB_SSLCERT and PLATFORM_DB_SSLKEY (config.LoadPostgresTLS).
// missing keys are recorded on e.
func loadServer(e *config.Env) Server {
	s := Server{
		Host:     e.String("PLATFORM_DB_HOST"),
		Port:     e.Int("PLATFORM_DB_PORT", 1, 65535),
		Database: e.String("PLATFORM_DB_NAME"),
		SSLMode:  e.String("PLATFORM_DB_SSLMODE"),
	}
	s.TLS = config.LoadPostgresTLS(e, s.SSLMode)
	return s
}

// check reports a value that is present but not acceptable.
func (s Server) check() error {
	if s.SSLMode != "" && !sslModes[s.SSLMode] {
		return &config.KeyError{Key: "PLATFORM_DB_SSLMODE", Reason: "must be one of disable, require, verify-ca, verify-full"}
	}
	return nil
}

// loadAdmin reads PLATFORM_DB_ADMIN_USER and PLATFORM_DB_ADMIN_PASSWORD, the
// login platformctl uses for migrations and role passwords. it must be a
// superuser, or a role that may create roles and set role to platform_owner.
func loadAdmin(e *config.Env) Login {
	return Login{
		User:     e.String("PLATFORM_DB_ADMIN_USER"),
		Password: e.Secret("PLATFORM_DB_ADMIN_PASSWORD", 1),
	}
}

func loadRole(e *config.Env, r Role) Login {
	return Login{User: string(r), Password: e.Secret(r.PasswordKey(), minRolePassword)}
}

// AdminConfig is what platformctl db migrate needs.
type AdminConfig struct {
	Server Server
	Admin  Login
}

// LoadAdminConfig reads the server and the admin login.
func LoadAdminConfig(e *config.Env) (AdminConfig, error) {
	c := AdminConfig{Server: loadServer(e), Admin: loadAdmin(e)}
	return c, errors.Join(e.Err(), c.Server.check())
}

// RolesConfig is what platformctl db roles needs.
type RolesConfig struct {
	Server Server
	Admin  Login
	Roles  []Login
}

// LoadRolesConfig reads the server, the admin login and the password of
// every application role.
func LoadRolesConfig(e *config.Env) (RolesConfig, error) {
	c := RolesConfig{Server: loadServer(e), Admin: loadAdmin(e)}
	for _, r := range AppRoles {
		c.Roles = append(c.Roles, loadRole(e, r))
	}
	return c, errors.Join(e.Err(), c.Server.check())
}

// PoolConfig is what a service needs to open pools for its roles.
type PoolConfig struct {
	Server Server
	Roles  []Login
	// MaxConns sets the size of a role's pool. a role not listed gets
	// pgxpool's default, max(4, number of cpus).
	MaxConns map[Role]int32
}

// Only returns the configuration for one of its roles, with that role's
// pool size, so a service can open a second, separate pool for the same
// role (the control plane's reconciler, review).
func (c PoolConfig) Only(r Role, maxConns int32) (PoolConfig, error) {
	for _, l := range c.Roles {
		if Role(l.User) == r {
			return PoolConfig{Server: c.Server, Roles: []Login{l}, MaxConns: map[Role]int32{r: maxConns}}, nil
		}
	}
	return PoolConfig{}, fmt.Errorf("db: role %s is not loaded", r)
}

// LoadPoolConfig reads the server and the passwords of the given roles only,
// so a service never holds the credentials of a role it does not use.
func LoadPoolConfig(e *config.Env, roles ...Role) (PoolConfig, error) {
	c := PoolConfig{Server: loadServer(e)}
	var errs []error
	for _, r := range roles {
		if !r.valid() {
			errs = append(errs, fmt.Errorf("db: unknown role %q", r))
			continue
		}
		c.Roles = append(c.Roles, loadRole(e, r))
	}
	if len(roles) == 0 {
		errs = append(errs, errors.New("db: no role requested"))
	}
	return c, errors.Join(append(errs, e.Err(), c.Server.check())...)
}

// connString builds a keyword/value connection string. values are quoted, so
// a password with spaces or quotes cannot inject other keywords.
func (s Server) connString(l Login, appName string) string {
	kv := [][2]string{
		{"host", s.Host},
		{"port", strconv.Itoa(s.Port)},
		{"dbname", s.Database},
		{"sslmode", s.SSLMode},
		{"user", l.User},
		{"password", l.Password.Reveal()},
		{"application_name", appName},
	}
	kv = append(kv, s.TLS.PGParams()...)
	var b strings.Builder
	for i, p := range kv {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s='%s'", p[0], strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(p[1]))
	}
	return b.String()
}
