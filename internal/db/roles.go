package db

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SetRolePasswords gives every application role in cfg its password and the
// login attribute, then logs in as each role to prove the password works.
// the roles must exist, which platformctl db migrate guarantees. it is
// idempotent: running it again sets the same passwords.
//
// the password reaches the server only as a scram-sha-256 verifier computed
// here, so the plain text never appears in server logs or pg_stat_activity.
func SetRolePasswords(ctx context.Context, cfg RolesConfig) error {
	for _, l := range cfg.Roles {
		if !Role(l.User).valid() {
			return fmt.Errorf("db: %s is not an application role", l.User)
		}
		if err := checkPasswordChars(l.Password.Reveal()); err != nil {
			return fmt.Errorf("db: %s: %w", Role(l.User).PasswordKey(), err)
		}
	}

	conn, err := pgx.Connect(ctx, cfg.Server.connString(cfg.Admin, "platformctl-roles"))
	if err != nil {
		return fmt.Errorf("db: connect as admin: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		for _, l := range cfg.Roles {
			var exists bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from pg_roles where rolname = $1)`, l.User).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("role %s does not exist; run platformctl db migrate first", l.User)
			}
			verifier, err := scramVerifier(l.Password.Reveal())
			if err != nil {
				return err
			}
			// alter role takes no bind parameters. the verifier holds only
			// base64, digits and the separators $ and :, so quoting it as a
			// literal is exact.
			stmt := "alter role " + pgx.Identifier{l.User}.Sanitize() + " with login password '" + verifier + "'"
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("set password of %s: %w", l.User, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("db: roles: %w", err)
	}

	for _, l := range cfg.Roles {
		c, err := pgx.Connect(ctx, cfg.Server.connString(l, "platformctl-roles"))
		if err != nil {
			return fmt.Errorf("db: log in as %s after setting its password: %w", l.User, err)
		}
		err = checkSessionRole(ctx, c, l.User)
		_ = c.Close(context.WithoutCancel(ctx))
		if err != nil {
			return err
		}
	}
	return nil
}

// checkPasswordChars allows printable ascii only. postgresql normalises
// passwords with saslprep before hashing; for printable ascii that is the
// identity, so the verifier computed here matches what the server expects.
func checkPasswordChars(pw string) error {
	for i := 0; i < len(pw); i++ {
		if pw[i] < 0x20 || pw[i] > 0x7e {
			return errors.New("must contain printable ascii characters only")
		}
	}
	return nil
}

// scram parameters match postgresql's defaults (scram_iterations = 4096,
// 16-byte salt)
const (
	scramIterations = 4096
	scramSaltLen    = 16
)

// scramVerifier returns the stored form of a password as defined in rfc 5802
// and written by postgresql: SCRAM-SHA-256$<iter>:<salt>$<storedkey>:<serverkey>.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, scramSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("scram salt: %w", err)
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, scramIterations, sha256.Size)
	if err != nil {
		return "", fmt.Errorf("scram key: %w", err)
	}
	clientKey := hmacSHA256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(salted, "Server Key")
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s",
		scramIterations, b64(salt), b64(storedKey[:]), b64(serverKey)), nil
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}
