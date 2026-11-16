// Package passkey verifies soropass passkeys: webauthn credentials whose
// es256 (secp256r1) key is the founding signer of a soropass smart account,
// a stellar contract account (C-address) that the soropass AccountFactory
// deploys for the passkey.
//
// soropass (github.com/justmert/soropass, @soropass/core) defines no
// server-side sign-in protocol; its account contract verifies a webauthn
// assertion on-chain, and its sdk ships the same checks for off-chain use
// (verifyClientDataJSON, verifyRpIdHash and verifyAssertionSignature in
// @soropass/core/sign). this package is those checks in go, bound to the
// console as the relying party:
//
//   - clientDataJSON has type webauthn.get, the challenge the control plane
//     issued, the console origin, and no cross-origin flag;
//   - authenticatorData's rp id hash is sha-256 of the console host, and
//     the user present and user verified flags are set (the soropass account
//     contract requires user verification at signing time, so a credential
//     that cannot produce it could never sign for the account);
//   - the signature is ecdsa p-256 over sha-256(authenticatorData ‖
//     sha-256(clientDataJSON)), the digest the contract's __check_auth hands
//     to the host's secp256r1_verify.
//
// AccountAddress derives the account's C-address offline the way
// deriveAccountAddress of @soropass/core/create does, so the address the
// console stores is the soropass account of exactly that key and
// credential.
//
// the same Verify serves any later passkey step (for example an operator
// step-up): issue a fresh challenge, read the account's stored public key,
// and call Verify with both.
package passkey

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// MainnetFactory is the soropass AccountFactory on the public network, the
// deployer of every soropass account the console accepts
// (DEFAULT_ACCOUNT_FACTORIES of @soropass/core 0.3.1).
const MainnetFactory = "CCCNRWMICVEMMUSBI7DL3IKB566QEOOQOLVDOAM5SLFDZ2KGUSRR3JVF"

// PublicKeyLen is the length of an uncompressed sec-1 p-256 key.
const PublicKeyLen = 65

// MaxCredentialIDLen bounds the base64url credential id: webauthn caps a
// credential id at 1023 bytes, 1364 base64url characters.
const MaxCredentialIDLen = 1364

// errors of Verify; each names the check that failed
var (
	ErrClientData   = errors.New("passkey: clientDataJSON is not a webauthn.get answer")
	ErrChallenge    = errors.New("passkey: the challenge is not the one issued")
	ErrOrigin       = errors.New("passkey: the origin is not the console")
	ErrCrossOrigin  = errors.New("passkey: the assertion was made in a cross-origin frame")
	ErrAuthData     = errors.New("passkey: authenticatorData is malformed")
	ErrRPID         = errors.New("passkey: the credential belongs to another relying party")
	ErrUserPresent  = errors.New("passkey: the user present flag is not set")
	ErrUserVerified = errors.New("passkey: the user verified flag is not set")
	ErrSignature    = errors.New("passkey: the signature does not verify")
	ErrPublicKey    = errors.New("passkey: the public key is not an uncompressed p-256 point")
	ErrCredentialID = errors.New("passkey: the credential id is not base64url")
)

// RelyingParty is the console as a webauthn relying party.
type RelyingParty struct {
	// ID is the rp id, the console host without a port
	ID string
	// Origin is the exact origin a browser reports for the console
	Origin string
}

// Assertion is one webauthn.get answer.
type Assertion struct {
	AuthenticatorData []byte
	ClientDataJSON    []byte
	// Signature is the der signature the authenticator returned
	Signature []byte
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin *bool  `json:"crossOrigin"`
}

const (
	flagUP = 0x01
	flagUV = 0x04
	// rp id hash, flags and the sign count
	authDataMinLen = 32 + 1 + 4
)

// Verify checks an assertion for challenge (the base64url string the
// control plane issued) against the credential's sec-1 public key.
func (rp RelyingParty) Verify(a Assertion, challenge string, publicKey []byte) error {
	pub, err := ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	var cd clientData
	if !utf8.Valid(a.ClientDataJSON) || json.Unmarshal(a.ClientDataJSON, &cd) != nil {
		return ErrClientData
	}
	if cd.Type != "webauthn.get" {
		return ErrClientData
	}
	if challenge == "" || subtle.ConstantTimeCompare([]byte(cd.Challenge), []byte(challenge)) != 1 {
		return ErrChallenge
	}
	if cd.Origin != rp.Origin {
		return ErrOrigin
	}
	if cd.CrossOrigin != nil && *cd.CrossOrigin {
		return ErrCrossOrigin
	}
	if len(a.AuthenticatorData) < authDataMinLen {
		return ErrAuthData
	}
	want := sha256.Sum256([]byte(rp.ID))
	if !bytes.Equal(a.AuthenticatorData[:32], want[:]) {
		return ErrRPID
	}
	flags := a.AuthenticatorData[32]
	if flags&flagUP == 0 {
		return ErrUserPresent
	}
	if flags&flagUV == 0 {
		return ErrUserVerified
	}
	if !ecdsa.VerifyASN1(pub, SignedDigest(a.AuthenticatorData, a.ClientDataJSON), a.Signature) {
		return ErrSignature
	}
	return nil
}

// SignedDigest is sha-256(authenticatorData ‖ sha-256(clientDataJSON)), what
// the authenticator signs and what the account contract verifies.
func SignedDigest(authData, clientDataJSON []byte) []byte {
	cdh := sha256.Sum256(clientDataJSON)
	h := sha256.New()
	h.Write(authData)
	h.Write(cdh[:])
	return h.Sum(nil)
}

// SignCount is the authenticator's signature counter. synced passkeys
// report 0 on every use, so the console does not use it to detect clones.
func SignCount(authData []byte) (uint32, error) {
	if len(authData) < authDataMinLen {
		return 0, ErrAuthData
	}
	return binary.BigEndian.Uint32(authData[33:37]), nil
}

// ParsePublicKey reads an uncompressed sec-1 p-256 key (0x04 ‖ x ‖ y), the
// form @soropass/core returns and the account contract stores. a point off
// the curve is refused.
func ParsePublicKey(sec1 []byte) (*ecdsa.PublicKey, error) {
	if len(sec1) != PublicKeyLen || sec1[0] != 0x04 {
		return nil, ErrPublicKey
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), sec1)
	if err != nil {
		return nil, ErrPublicKey
	}
	return pub, nil
}

// ValidCredentialID reports whether id is an unpadded base64url webauthn
// credential id of 1 to 1023 bytes.
func ValidCredentialID(id string) bool {
	if id == "" || len(id) > MaxCredentialIDLen {
		return false
	}
	_, err := base64.RawURLEncoding.Strict().DecodeString(id)
	return err == nil
}

// AccountAddress is the C-address of the soropass account that factory
// deploys for the passkey on the network of passphrase: the factory deploys
// with salt sha-256(credential id ‖ public key), where the credential id is
// the utf-8 of its base64url form, so the address is bound to both and no
// one else can deploy an account of another key at it.
func AccountAddress(factory, credentialID string, publicKey []byte, passphrase string) (string, error) {
	if !ValidCredentialID(credentialID) {
		return "", ErrCredentialID
	}
	if _, err := ParsePublicKey(publicKey); err != nil {
		return "", err
	}
	raw, err := strkey.Decode(strkey.VersionByteContract, factory)
	if err != nil {
		return "", fmt.Errorf("passkey: factory %q is not a contract address: %w", factory, err)
	}
	var fid xdr.ContractId
	copy(fid[:], raw)
	salt := sha256.Sum256(append([]byte(credentialID), publicKey...))
	preimage := xdr.HashIdPreimage{
		Type: xdr.EnvelopeTypeEnvelopeTypeContractId,
		ContractId: &xdr.HashIdPreimageContractId{
			NetworkId: sha256.Sum256([]byte(passphrase)),
			ContractIdPreimage: xdr.ContractIdPreimage{
				Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
				FromAddress: &xdr.ContractIdPreimageFromAddress{
					Address: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &fid},
					Salt:    xdr.Uint256(salt),
				},
			},
		},
	}
	bin, err := preimage.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("passkey: encode preimage: %w", err)
	}
	id := sha256.Sum256(bin)
	return strkey.Encode(strkey.VersionByteContract, id[:])
}

// ValidContractAddress reports whether s is a contract strkey (C-address)
// with a correct checksum.
func ValidContractAddress(s string) bool {
	_, err := strkey.Decode(strkey.VersionByteContract, s)
	return err == nil
}
