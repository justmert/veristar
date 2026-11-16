package stepup

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/db/systemq"
)

// Passkey is a registered credential as the console lists it. the public
// key and the counter stay in the database.
type Passkey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// BackupEligible is the authenticator's backup eligibility flag: true for
	// a passkey a platform syncs between devices, false for a credential
	// bound to one authenticator such as a security key
	BackupEligible bool `json:"backup_eligible"`
}

func passkeyOf(row systemq.CpOperatorPasskey) (Passkey, webauthn.Credential, error) {
	var c webauthn.Credential
	if err := json.Unmarshal(row.Credential, &c); err != nil {
		return Passkey{}, c, fmt.Errorf("stepup: credential %s: %w", row.ID, err)
	}
	p := Passkey{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt.UTC(), BackupEligible: c.Flags.BackupEligible}
	if row.LastUsedAt != nil {
		t := row.LastUsedAt.UTC()
		p.LastUsedAt = &t
	}
	return p, c, nil
}

// LoadAccount reads the account's credentials inside tx (cp_system).
func LoadAccount(ctx context.Context, tx pgx.Tx, u session.User) (Account, []Passkey, error) {
	rows, err := systemq.New(tx).ListPasskeys(ctx, u.ID)
	if err != nil {
		return Account{}, nil, fmt.Errorf("stepup: list passkeys: %w", err)
	}
	acc := Account{User: u, Credentials: make([]webauthn.Credential, 0, len(rows))}
	list := make([]Passkey, 0, len(rows))
	for _, r := range rows {
		p, c, err := passkeyOf(r)
		if err != nil {
			return Account{}, nil, err
		}
		acc.Credentials = append(acc.Credentials, c)
		list = append(list, p)
	}
	return acc, list, nil
}

// AddPasskey stores a verified credential under name inside tx.
func AddPasskey(ctx context.Context, tx pgx.Tx, userID uuid.UUID, name string, c *webauthn.Credential) (Passkey, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return Passkey{}, fmt.Errorf("stepup: encode credential: %w", err)
	}
	row, err := systemq.New(tx).AddPasskey(ctx, systemq.AddPasskeyParams{
		UserID: userID, CredentialID: c.ID, Credential: b, Name: name,
	})
	if err != nil {
		return Passkey{}, fmt.Errorf("stepup: add passkey: %w", err)
	}
	p, _, err := passkeyOf(row)
	return p, err
}

// TouchPasskey stores the credential after an assertion: its counter and
// flags, and the time of use.
func TouchPasskey(ctx context.Context, tx pgx.Tx, userID uuid.UUID, c *webauthn.Credential) error {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("stepup: encode credential: %w", err)
	}
	n, err := systemq.New(tx).TouchPasskey(ctx, systemq.TouchPasskeyParams{Credential: b, UserID: userID, CredentialID: c.ID})
	if err != nil {
		return fmt.Errorf("stepup: touch passkey: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("stepup: passkey of the assertion is gone")
	}
	return nil
}
