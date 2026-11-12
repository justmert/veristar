package events_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/justmert/stellar-analytics/internal/events"
)

type proofVector struct {
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

func leadingZeroBits(b []byte) int {
	n := 0
	for _, x := range b {
		if x != 0 {
			return n + bits.LeadingZeros8(x)
		}
		n += 8
	}
	return n
}

// TestTrackerProofVectors parses the tracker's own proof vectors as wire
// bodies, so the go parser accepts exactly what tracker release 1.0.0 sends,
// and checks the vector arithmetic (tag and digest) from the go side.
func TestTrackerProofVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "packages", "tracker", "test", "vectors", "proof-v1.json"))
	require.NoError(t, err)
	var file struct {
		Vectors []proofVector `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Vectors)

	for i, v := range file.Vectors {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			key, err := hex.DecodeString(v.ProofKeyHex)
			require.NoError(t, err)
			require.Equal(t, v.W+"|"+strconv.FormatInt(v.IAT, 10)+"|"+v.Salt+"|"+strconv.Itoa(v.Bits), v.TagInput)
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(v.TagInput))
			require.Equal(t, v.Tag, hex.EncodeToString(mac.Sum(nil)))
			require.Equal(t, v.W+"|"+strconv.FormatInt(v.IAT, 10)+"|"+v.Salt+"|"+strconv.FormatUint(v.Nonce, 10), v.ProofInput)
			digest := sha256.Sum256([]byte(v.ProofInput))
			require.Equal(t, v.Digest, hex.EncodeToString(digest[:]))
			require.GreaterOrEqual(t, leadingZeroBits(digest[:]), v.Bits)

			b, err := json.Marshal(map[string]any{
				"v": 1, "w": v.W, "t": "pv", "h": "[::1]", "u": "/", "r": "", "ti": "", "s": "1920x1080",
				"l": "en-US", "ts": 1794528001234,
				"p": map[string]any{"iat": v.IAT, "salt": v.Salt, "bits": v.Bits, "tag": v.Tag, "nonce": v.Nonce},
			})
			require.NoError(t, err)
			w, err := events.ParseWire(b)
			require.NoError(t, err)
			require.Equal(t, v.Salt, hex.EncodeToString(w.Proof.Salt[:]))
			require.Equal(t, v.Tag, hex.EncodeToString(w.Proof.Tag[:]))
			require.Equal(t, v.Nonce, w.Proof.Nonce)
			require.Equal(t, v.Bits, w.Proof.Bits)
			require.Equal(t, v.IAT, w.Proof.IAT)
		})
	}
}
