package stepup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/passkey"
	"github.com/justmert/veristar/internal/db/systemq"
)

// Passkey is a passkey the account added for the step-up, as the console
// lists it. the key stays in the database.
type Passkey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// Keys are the credentials an account can step up with: its added passkeys
// and, when the roster names its address, its sign-in passkey, by
// credential id.
type Keys struct {
	Added []Passkey
	// SignIn is true when the account's sign-in passkey (4.1.9) can step
	// up: an active roster row names its address
	SignIn   bool
	signInID string
	byID     map[string][]byte
}

// Count is how many credentials can step up.
func (k Keys) Count() int64 {
	n := int64(len(k.Added))
	if k.SignIn {
		n++
	}
	return n
}

// LoadKeys reads the account's step-up credentials inside tx (cp_system).
func LoadKeys(ctx context.Context, tx pgx.Tx, user uuid.UUID) (Keys, error) {
	q := systemq.New(tx)
	rows, err := q.ListPasskeys(ctx, user)
	if err != nil {
		return Keys{}, fmt.Errorf("stepup: list passkeys: %w", err)
	}
	k := Keys{Added: make([]Passkey, 0, len(rows)), byID: map[string][]byte{}}
	for _, r := range rows {
		p := Passkey{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt.UTC()}
		if r.LastUsedAt != nil {
			t := r.LastUsedAt.UTC()
			p.LastUsedAt = &t
		}
		k.Added = append(k.Added, p)
		k.byID[r.CredentialID] = r.PublicKey
	}
	si, err := q.SignInPasskey(ctx, user)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Keys{}, fmt.Errorf("stepup: sign-in passkey: %w", err)
	}
	if err == nil && si.PasskeyCredentialID != nil && si.StepUp {
		k.SignIn, k.signInID = true, *si.PasskeyCredentialID
		k.byID[*si.PasskeyCredentialID] = si.PasskeyPublicKey
	}
	return k, nil
}

// Answer is a webauthn.get answer in the form of 4.1.9, every byte field
// base64url without padding. PublicKey is set only when a new passkey is
// registered: its first assertion, signed by that key, proves the person
// holds it.
type Answer struct {
	CredentialID      *string `json:"credential_id"`
	AuthenticatorData *string `json:"authenticator_data"`
	ClientDataJSON    *string `json:"client_data_json"`
	Signature         *string `json:"signature"`
	PublicKey         *string `json:"public_key"`
}

// the field bounds of passkey sign-in (4.1.9)
const (
	maxAuthDataLen   = 1024
	maxClientDataLen = 4096
	maxSignatureLen  = 80
)

func decodeField(fields map[string]string, name string, v *string, max int, required bool) []byte {
	if v == nil || *v == "" {
		if required {
			fields[name] = httpx.FieldRequired
		}
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(*v)
	if err != nil || len(b) == 0 {
		fields[name] = httpx.FieldInvalidFormat
		return nil
	}
	if len(b) > max {
		fields[name] = httpx.FieldTooLong
		return nil
	}
	return b
}

// decoded checks the fields of a; withKey requires the public key of a new
// passkey. fields collects the 422 field codes.
func (a Answer) decoded(fields map[string]string, withKey bool) (passkey.Assertion, string, []byte) {
	var id string
	switch {
	case a.CredentialID == nil || *a.CredentialID == "":
		fields["credential_id"] = httpx.FieldRequired
	case !passkey.ValidCredentialID(*a.CredentialID):
		fields["credential_id"] = httpx.FieldInvalidFormat
	default:
		id = *a.CredentialID
	}
	as := passkey.Assertion{
		AuthenticatorData: decodeField(fields, "authenticator_data", a.AuthenticatorData, maxAuthDataLen, true),
		ClientDataJSON:    decodeField(fields, "client_data_json", a.ClientDataJSON, maxClientDataLen, true),
		Signature:         decodeField(fields, "signature", a.Signature, maxSignatureLen, true),
	}
	var pub []byte
	if withKey {
		pub = decodeField(fields, "public_key", a.PublicKey, passkey.PublicKeyLen, true)
		if pub != nil {
			if _, err := passkey.ParsePublicKey(pub); err != nil {
				fields["public_key"] = httpx.FieldInvalidValue
			}
		}
	} else if a.PublicKey != nil {
		fields["public_key"] = httpx.FieldInvalidValue
	}
	return as, id, pub
}

// AddPasskey stores a verified passkey under name inside tx.
func AddPasskey(ctx context.Context, tx pgx.Tx, user uuid.UUID, name, credentialID string, publicKey []byte) (Passkey, error) {
	row, err := systemq.New(tx).AddPasskey(ctx, systemq.AddPasskeyParams{
		UserID: user, CredentialID: credentialID, PublicKey: publicKey, Name: name,
	})
	if err != nil {
		return Passkey{}, fmt.Errorf("stepup: add passkey: %w", err)
	}
	return Passkey{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt.UTC()}, nil
}
