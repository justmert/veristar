package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/events"
	"github.com/justmert/stellar-analytics/tests/fixtures/natsctr"
)

type pfVector struct {
	ProofKeyHex string `json:"proofKeyHex"`
	W           string `json:"w"`
	IAT         int64  `json:"iat"`
	Salt        string `json:"salt"`
	Bits        int    `json:"bits"`
	TagInput    string `json:"tagInput"`
	Tag         string `json:"tag"`
	Nonce       uint64 `json:"nonce"`
	ProofInput  string `json:"proofInput"`
	Digest      string `json:"digest"`
}

func pfVectors(t *testing.T) []pfVector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(natsctr.RepoRoot(t), "packages", "tracker", "test", "vectors", "proof-v1.json"))
	require.NoError(t, err)
	var file struct {
		Vectors []pfVector `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Vectors)
	return file.Vectors
}

func (v pfVector) proof(t *testing.T) events.Proof {
	t.Helper()
	p := events.Proof{IAT: v.IAT, Bits: v.Bits, Nonce: v.Nonce}
	_, err := hex.Decode(p.Salt[:], []byte(v.Salt))
	require.NoError(t, err)
	_, err = hex.Decode(p.Tag[:], []byte(v.Tag))
	require.NoError(t, err)
	return p
}

// TestTrackerVectorBytes checks the gateway's tag input, tag, proof input,
// digest and work count against every tracker vector, byte for byte.
func TestTrackerVectorBytes(t *testing.T) {
	for i, v := range pfVectors(t) {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			key, err := hex.DecodeString(v.ProofKeyHex)
			require.NoError(t, err)
			p := v.proof(t)
			require.Equal(t, v.TagInput, string(tagInput(v.W, v.IAT, p.Salt[:], v.Bits)))
			require.Equal(t, v.ProofInput, string(proofInput(v.W, v.IAT, p.Salt[:], v.Nonce)))

			ks := &KeyStore{keys: map[string]*cachedKey{}, refresh: time.Hour}
			now := time.Unix(v.IAT, 0)
			ks.keys[ProofKeyName(now)] = &cachedKey{key: key, fetched: now}
			tag, err := ks.mac(t.Context(), now, now, false, tagInput(v.W, v.IAT, p.Salt[:], v.Bits))
			require.NoError(t, err)
			require.Equal(t, v.Tag, hex.EncodeToString(tag))

			d := sha256.Sum256(proofInput(v.W, v.IAT, p.Salt[:], v.Nonce))
			require.Equal(t, v.Digest, hex.EncodeToString(d[:]))
			require.GreaterOrEqual(t, leadingZeroBits(d[:]), v.Bits)
			// the vector nonce is the smallest, so every smaller one fails
			for n := range v.Nonce {
				d := sha256.Sum256(proofInput(v.W, v.IAT, p.Salt[:], n))
				require.Less(t, leadingZeroBits(d[:]), v.Bits)
			}
		})
	}
}

// TestTrackerVectorsVerify stores each vector's key in a real edge_secrets
// bucket under the vector's day and runs the full verifier on it, then
// checks that each part of the proof is bound: site, tag, nonce, bits.
func TestTrackerVectorsVerify(t *testing.T) {
	n := pfStartNATS(t)
	ctx := t.Context()
	for i, v := range pfVectors(t) {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			key, err := hex.DecodeString(v.ProofKeyHex)
			require.NoError(t, err)
			iat := time.Unix(v.IAT, 0)
			name := ProofKeyName(iat)
			require.NoError(t, n.kv.Purge(ctx, name))
			_, err = n.kv.Create(ctx, name, key)
			require.NoError(t, err)

			clock := &pfClock{t: iat.Add(time.Hour)}
			ver, err := NewProofVerifier(pfKeyStore(ctx, t, n.js, clock), 50, 1000)
			require.NoError(t, err)
			p := v.proof(t)
			require.NoError(t, ver.Verify(ctx, v.W, p))

			// another site: the tag covers w
			require.Equal(t, ReasonChallengeTag, pfReason(t, ver.Verify(ctx, uuid.NewString(), p)))
			wrongTag := p
			wrongTag.Tag[31] ^= 1
			require.Equal(t, ReasonChallengeTag, pfReason(t, ver.Verify(ctx, v.W, wrongTag)))
			// lowering bits breaks the tag, so difficulty cannot be chosen by the client
			if v.Bits > 0 {
				easier := p
				easier.Bits = 0
				require.Equal(t, ReasonChallengeTag, pfReason(t, ver.Verify(ctx, v.W, easier)))
				lazy := p
				lazy.Nonce = 0
				require.Equal(t, ReasonProofWork, pfReason(t, ver.Verify(ctx, v.W, lazy)))
			}
			tooHard := p
			tooHard.Bits = MaxProofBits + 1
			require.Equal(t, ReasonChallengeTag, pfReason(t, ver.Verify(ctx, v.W, tooHard)))

			// too old and from the future, by server time
			clock.Set(iat.Add(ChallengeTTL + time.Second))
			require.Equal(t, ReasonChallengeExpired, pfReason(t, ver.Verify(ctx, v.W, p)))
			clock.Set(iat.Add(-MaxClockSkew - time.Second))
			require.Equal(t, ReasonChallengeFuture, pfReason(t, ver.Verify(ctx, v.W, p)))
		})
	}
}

// TestReplayLimit checks the per-proof limit, that refused proofs do not
// count, the bounded table, and the limit under concurrency.
func TestReplayLimit(t *testing.T) {
	n := pfStartNATS(t)
	ctx := t.Context()
	clock := &pfClock{t: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	ks := pfKeyStore(ctx, t, n.js, clock)
	ch, err := NewChallenger(ks, 4)
	require.NoError(t, err)
	site := uuid.NewString()

	const limit = 3
	ver, err := NewProofVerifier(ks, limit, 2)
	require.NoError(t, err)
	p1 := pfSolve(t, pfIssue(t, ch, site))
	forged := p1
	forged.Tag[0] ^= 0xff
	// forged copies are refused before the replay table and use nothing up
	for range 10 {
		require.Equal(t, ReasonChallengeTag, pfReason(t, ver.Verify(ctx, site, forged)))
	}
	for range limit {
		require.NoError(t, ver.Verify(ctx, site, p1))
	}
	require.Equal(t, ReasonReplay, pfReason(t, ver.Verify(ctx, site, p1)))
	require.Equal(t, ReasonReplay, pfReason(t, ver.Verify(ctx, site, p1)))

	// another page load of the same visitor has its own allowance; any
	// other valid nonce for p1's challenge is still p1
	p2 := pfSolve(t, pfIssue(t, ch, site))
	require.NoError(t, ver.Verify(ctx, site, p2))
	otherNonce := p1
	for nonce := p1.Nonce + 1; ; nonce++ {
		d := sha256.Sum256(proofInput(site, p1.IAT, p1.Salt[:], nonce))
		if leadingZeroBits(d[:]) >= p1.Bits {
			otherNonce.Nonce = nonce
			break
		}
	}
	require.Equal(t, ReasonReplay, pfReason(t, ver.Verify(ctx, site, otherNonce)))

	// the table holds two proofs; a third pushes out the least recent (p2,
	// because p1 was used last), which then counts from zero again. this is
	// the bounded-memory limitation browser-proof.md describes.
	p3 := pfSolve(t, pfIssue(t, ch, site))
	require.NoError(t, ver.Verify(ctx, site, p3))
	require.Equal(t, 2, ver.replay.size())
	require.Equal(t, ReasonReplay, pfReason(t, ver.Verify(ctx, site, p1)))
	for range limit {
		require.NoError(t, ver.Verify(ctx, site, p2))
	}

	// concurrent replays of one proof: exactly limit succeed
	wide, err := NewProofVerifier(ks, 50, 1000)
	require.NoError(t, err)
	p4 := pfSolve(t, pfIssue(t, ch, site))
	var ok atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			if wide.Verify(ctx, site, p4) == nil {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int64(50), ok.Load())

	_, err = NewProofVerifier(ks, 0, 10)
	require.Error(t, err)
	_, err = NewProofVerifier(ks, 1, 0)
	require.Error(t, err)
	_, err = NewProofVerifier(nil, 1, 1)
	require.Error(t, err)
}

func TestLeadingZeroBits(t *testing.T) {
	for _, c := range []struct {
		in   []byte
		want int
	}{
		{[]byte{0x80}, 0},
		{[]byte{0x01}, 7},
		{[]byte{0x00, 0x40}, 9},
		{[]byte{0x00, 0x00}, 16},
		{[]byte{0x00, 0x00, 0x0f}, 20},
	} {
		require.Equal(t, c.want, leadingZeroBits(c.in), "%x", c.in)
	}
}
