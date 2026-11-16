package passkey

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/network"
)

func b64(s string) []byte {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// a real p-256 webauthn.get answer, the fixture @soropass/core tests its
// verification with (src/__fixtures__/realAssertion.ts). its flags carry
// user presence only.
var real = struct {
	authData, clientData, sig, pub []byte
	rpID, origin, challenge        string
}{
	authData:   b64("PdxHEOnAiLIp26idVjIguzn3Ipr_RlsKZWsa-5qK-KABAAAAkA"),
	clientData: b64("eyJjaGFsbGVuZ2UiOiJkRzkwWVd4c2VWVnVhWEYxWlZaaGJIVmxSWFpsY25sVWFXMWwiLCJjbGllbnRFeHRlbnNpb25zIjp7fSwiaGFzaEFsZ29yaXRobSI6IlNIQS0yNTYiLCJvcmlnaW4iOiJodHRwczovL2Rldi5kb250bmVlZGEucHciLCJ0eXBlIjoid2ViYXV0aG4uZ2V0In0"),
	sig:        b64("MEUCIQDYXBOpCWSWq2Ll4558GJKD2RoWg958lvJSB_GdeokxogIgWuEVQ7ee6AswQY0OsuQ6y8Ks6jhd45bDx92wjXKs900"),
	pub:        unhex("04885e169faee86bc54f62cd1a8bdfdd9ad3d2215506c03fefaad1cacc511bd035f1619e03a84f9a769bd07015894615464c1335c37bed005ffd147476fde522f4"),
	rpID:       "dev.dontneeda.pw",
	origin:     "https://dev.dontneeda.pw",
	challenge:  "dG90YWxseVVuaXF1ZVZhbHVlRXZlcnlUaW1l",
}

func TestRealAssertion(t *testing.T) {
	// the digest soropass pins for this fixture
	if got := hex.EncodeToString(SignedDigest(real.authData, real.clientData)); got != "5090551651fdef7652709d3dcc5dc32f4141eae1e5f5b27871a9d2cc46e5874f" {
		t.Fatalf("digest %s", got)
	}
	pub, err := ParsePublicKey(real.pub)
	if err != nil {
		t.Fatal(err)
	}
	if !ecdsa.VerifyASN1(pub, SignedDigest(real.authData, real.clientData), real.sig) {
		t.Fatal("the real signature does not verify")
	}
	rp := RelyingParty{ID: real.rpID, Origin: real.origin}
	// every check up to the flags passes; the fixture has no user
	// verification, which the console requires
	err = rp.Verify(Assertion{AuthenticatorData: real.authData, ClientDataJSON: real.clientData, Signature: real.sig},
		real.challenge, real.pub)
	if !errors.Is(err, ErrUserVerified) {
		t.Fatalf("want ErrUserVerified, got %v", err)
	}
}

// authenticator is a software passkey for the tests
type authenticator struct {
	key *ecdsa.PrivateKey
	pub []byte
}

func newAuthenticator(t *testing.T) authenticator {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return authenticator{key: k, pub: pub}
}

type ceremony struct {
	typ, challenge, origin, rpID string
	crossOrigin                  *bool
	flags                        byte
}

func (a authenticator) sign(t *testing.T, c ceremony) Assertion {
	t.Helper()
	cd := map[string]any{"type": c.typ, "challenge": c.challenge, "origin": c.origin}
	if c.crossOrigin != nil {
		cd["crossOrigin"] = *c.crossOrigin
	}
	cdj, err := json.Marshal(cd)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(c.rpID))
	ad := append(h[:], c.flags, 0, 0, 0, 7)
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, SignedDigest(ad, cdj))
	if err != nil {
		t.Fatal(err)
	}
	return Assertion{AuthenticatorData: ad, ClientDataJSON: cdj, Signature: sig}
}

func TestVerify(t *testing.T) {
	rp := RelyingParty{ID: "app.veristar.test", Origin: "https://app.veristar.test"}
	auth := newAuthenticator(t)
	other := newAuthenticator(t)
	const challenge = "q2Vy7oZ2yq1mJ8u0Yb4tU0c0qkqQm2m5h3G8n9w6m2E"
	good := ceremony{typ: "webauthn.get", challenge: challenge, origin: rp.Origin, rpID: rp.ID, flags: flagUP | flagUV}
	yes, no := true, false

	cases := []struct {
		name string
		mod  func(*ceremony)
		pub  []byte
		want error
	}{
		{"valid", func(*ceremony) {}, auth.pub, nil},
		{"cross origin false is fine", func(c *ceremony) { c.crossOrigin = &no }, auth.pub, nil},
		{"another key", func(*ceremony) {}, other.pub, ErrSignature},
		{"registration answer", func(c *ceremony) { c.typ = "webauthn.create" }, auth.pub, ErrClientData},
		{"another challenge", func(c *ceremony) { c.challenge = "AAAA" }, auth.pub, ErrChallenge},
		{"another origin", func(c *ceremony) { c.origin = "https://evil.test" }, auth.pub, ErrOrigin},
		{"http origin", func(c *ceremony) { c.origin = "http://app.veristar.test" }, auth.pub, ErrOrigin},
		{"cross origin frame", func(c *ceremony) { c.crossOrigin = &yes }, auth.pub, ErrCrossOrigin},
		{"another rp id", func(c *ceremony) { c.rpID = "veristar.test" }, auth.pub, ErrRPID},
		{"no user presence", func(c *ceremony) { c.flags = flagUV }, auth.pub, ErrUserPresent},
		{"no user verification", func(c *ceremony) { c.flags = flagUP }, auth.pub, ErrUserVerified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mod(&c)
			err := rp.Verify(auth.sign(t, c), challenge, tc.pub)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}

	t.Run("tampered authenticator data", func(t *testing.T) {
		a := auth.sign(t, good)
		a.AuthenticatorData[36]++
		if err := rp.Verify(a, challenge, auth.pub); !errors.Is(err, ErrSignature) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("short authenticator data", func(t *testing.T) {
		a := auth.sign(t, good)
		a.AuthenticatorData = a.AuthenticatorData[:36]
		if err := rp.Verify(a, challenge, auth.pub); !errors.Is(err, ErrAuthData) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("client data not json", func(t *testing.T) {
		a := auth.sign(t, good)
		a.ClientDataJSON = []byte("{")
		if err := rp.Verify(a, challenge, auth.pub); !errors.Is(err, ErrClientData) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty expected challenge", func(t *testing.T) {
		c := good
		c.challenge = ""
		if err := rp.Verify(auth.sign(t, c), "", auth.pub); !errors.Is(err, ErrChallenge) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("compressed key", func(t *testing.T) {
		compressed := elliptic.MarshalCompressed(elliptic.P256(), auth.key.X, auth.key.Y)
		if err := rp.Verify(auth.sign(t, good), challenge, compressed); !errors.Is(err, ErrPublicKey) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("point off the curve", func(t *testing.T) {
		bad := append([]byte(nil), auth.pub...)
		bad[64] ^= 1
		if err := rp.Verify(auth.sign(t, good), challenge, bad); !errors.Is(err, ErrPublicKey) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestSignCount(t *testing.T) {
	n, err := SignCount(real.authData)
	if err != nil || n != 0x90 {
		t.Fatalf("got %d %v", n, err)
	}
	if _, err := SignCount(real.authData[:10]); !errors.Is(err, ErrAuthData) {
		t.Fatalf("got %v", err)
	}
}

func TestAccountAddress(t *testing.T) {
	kat1 := unhex("041f140146bfb1b251f84f4ddbe0d4cdcfd77afd984a9520e35794021f8312bb9eec995a08b1fa7704df3dcc0b50a9665263fb7711f95f9f8a449c5096e47c892b")
	// the cross-language known answer of soropass: the rust account-factory
	// test address_kat_matches_sdk_derivation and the typescript
	// deriveAccountAddress land on this address
	got, err := AccountAddress("CDBOW57Z4PY3SWFMTVPODG33JC55CKDFAFPH5EVB5APVPL7KMMO5OO4X", "democred", kat1,
		network.TestNetworkPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if got != "CDLHJTPY4QU74EGLADUTZQCKLINANILFLE5E4OF46QJXKBA7QC7IV4KQ" {
		t.Fatalf("testnet kat: %s", got)
	}
	// the mainnet factory, computed with deriveAccountAddress of
	// @soropass/core 0.3.1 and its default factory for the public network
	got, err = AccountAddress(MainnetFactory, "ZGVtby1jcmVkZW50aWFs", kat1, network.PublicNetworkPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if got != "CBH2J5X4WBV2KT6NSUE47DRFBUHTVJOBJTBGGNELJ2DZXTDB3AYRAXAO" {
		t.Fatalf("mainnet: %s", got)
	}
	if !ValidContractAddress(got) || ValidContractAddress("GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN") {
		t.Fatal("contract address check")
	}

	other := newAuthenticator(t)
	if b, _ := AccountAddress(MainnetFactory, "ZGVtby1jcmVkZW50aWFs", other.pub, network.PublicNetworkPassphrase); b == got {
		t.Fatal("the address does not depend on the key")
	}
	if _, err := AccountAddress(MainnetFactory, "not base64url!", kat1, network.PublicNetworkPassphrase); !errors.Is(err, ErrCredentialID) {
		t.Fatalf("got %v", err)
	}
	if _, err := AccountAddress(MainnetFactory, "", kat1, network.PublicNetworkPassphrase); !errors.Is(err, ErrCredentialID) {
		t.Fatalf("got %v", err)
	}
	if _, err := AccountAddress(MainnetFactory, "democred", kat1[:64], network.PublicNetworkPassphrase); !errors.Is(err, ErrPublicKey) {
		t.Fatalf("got %v", err)
	}
	if _, err := AccountAddress("GCB634SLKV5N4COK4FICWJADNTFWIHTWWM34BFPLACL6YSQVLY5ZWOQN", "democred", kat1,
		network.PublicNetworkPassphrase); err == nil {
		t.Fatal("an account id is not a factory")
	}
}

func TestValidCredentialID(t *testing.T) {
	for id, want := range map[string]bool{
		"democred":                 true,
		"AQIDBA":                   true,
		"":                         false,
		"AQIDBA==":                 false,
		"AQ+DBA":                   false,
		string(make([]byte, 1365)): false,
	} {
		if ValidCredentialID(id) != want {
			t.Errorf("%q: want %v", id, want)
		}
	}
}
