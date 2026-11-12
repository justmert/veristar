package gateway

import (
	"container/list"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"sync"
	"time"

	"github.com/justmert/stellar-analytics/internal/events"
)

// proofInput is w|iat|salt|nonce, salt as lowercase hex (docs/reference/wire-v1.md).
func proofInput(w string, iat int64, salt []byte, nonce uint64) []byte {
	b := make([]byte, 0, len(w)+80)
	b = append(b, w...)
	b = append(b, '|')
	b = strconv.AppendInt(b, iat, 10)
	b = append(b, '|')
	b = hex.AppendEncode(b, salt)
	b = append(b, '|')
	return strconv.AppendUint(b, nonce, 10)
}

// leadingZeroBits counts from the most significant bit of the first byte,
// as packages/tracker/src/proof.ts does.
func leadingZeroBits(digest []byte) int {
	n := 0
	for _, b := range digest {
		if b != 0 {
			return n + bits.LeadingZeros8(b)
		}
		n += 8
	}
	return n
}

// ProofVerifier checks the proof every event carries: age, tag, work and
// the per-node replay limit.
type ProofVerifier struct {
	keys   *KeyStore
	replay *replayLRU
}

// NewProofVerifier takes the replay limit (events per proof) and how many
// proofs the replay table remembers. the table is bounded so forged traffic
// cannot grow memory; a proof pushed out of it starts counting again, which
// is part of the limitation docs/reference/browser-proof.md states.
func NewProofVerifier(keys *KeyStore, replayLimit, replayEntries int) (*ProofVerifier, error) {
	if keys == nil {
		return nil, errors.New("gateway: proof verifier needs a key store")
	}
	if replayLimit < 1 {
		return nil, fmt.Errorf("gateway: replay limit %d must be at least 1", replayLimit)
	}
	if replayEntries < 1 {
		return nil, fmt.Errorf("gateway: replay table size %d must be at least 1", replayEntries)
	}
	return &ProofVerifier{keys: keys, replay: newReplayLRU(replayLimit, replayEntries)}, nil
}

// Verify checks the proof of an event for siteID, the event's w field. the
// tag covers w, so a proof issued for another site fails as a wrong tag.
// the replay counter moves only for proofs that passed every other check,
// so forged proofs cannot use up a real visitor's allowance.
func (v *ProofVerifier) Verify(ctx context.Context, siteID string, p events.Proof) error {
	now := v.keys.now()
	issued := time.Unix(p.IAT, 0)
	if issued.After(now.Add(MaxClockSkew)) {
		return reject(ReasonChallengeFuture)
	}
	if now.Sub(issued) > ChallengeTTL {
		return reject(ReasonChallengeExpired)
	}
	if p.Bits > MaxProofBits {
		// never issued, so the tag cannot match; refused before any key
		// lookup
		return reject(ReasonChallengeTag)
	}
	tag, err := v.keys.mac(ctx, issued, now, false, tagInput(siteID, p.IAT, p.Salt[:], p.Bits))
	switch {
	case errors.Is(err, ErrNoProofKey), errors.Is(err, ErrBadProofKey):
		// the day's key is gone, for example after a whole-cluster
		// restart, so no challenge signed with it can be checked
		return reject(ReasonChallengeTag)
	case err != nil:
		return &RejectError{Reason: ReasonUnavailable, Err: err}
	}
	if !hmac.Equal(tag, p.Tag[:]) {
		return reject(ReasonChallengeTag)
	}
	digest := sha256.Sum256(proofInput(siteID, p.IAT, p.Salt[:], p.Nonce))
	if leadingZeroBits(digest[:]) < p.Bits {
		return reject(ReasonProofWork)
	}
	// the tag identifies the challenge: one page load, whatever nonce it
	// found
	if !v.replay.allow(p.Tag) {
		return reject(ReasonReplay)
	}
	return nil
}

// replayLRU counts uses per proof in a bounded least recently used table.
type replayLRU struct {
	limit   int
	entries int

	mu    sync.Mutex
	order *list.List // front is most recent; values are *replayEntry
	index map[[32]byte]*list.Element
}

type replayEntry struct {
	tag   [32]byte
	count int
}

func newReplayLRU(limit, entries int) *replayLRU {
	return &replayLRU{limit: limit, entries: entries, order: list.New(), index: make(map[[32]byte]*list.Element)}
}

// allow counts one use and reports whether it is within the limit. refused
// uses are not counted, so the count stays at the limit.
func (r *replayLRU) allow(tag [32]byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if el, ok := r.index[tag]; ok {
		r.order.MoveToFront(el)
		e := el.Value.(*replayEntry) //nolint:errcheck,forcetypeassert // only *replayEntry is stored
		if e.count >= r.limit {
			return false
		}
		e.count++
		return true
	}
	r.index[tag] = r.order.PushFront(&replayEntry{tag: tag, count: 1})
	if r.order.Len() > r.entries {
		oldest := r.order.Back()
		r.order.Remove(oldest)
		delete(r.index, oldest.Value.(*replayEntry).tag) //nolint:errcheck,forcetypeassert // only *replayEntry is stored
	}
	return true
}

// size reports how many proofs the table remembers.
func (r *replayLRU) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.order.Len()
}
