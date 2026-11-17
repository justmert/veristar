package replayer

import (
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/justmert/veristar/internal/events"
	"github.com/justmert/veristar/internal/obs"
)

func loads(owners []string) map[string]int {
	out := map[string]int{}
	for _, o := range owners {
		out[o]++
	}
	return out
}

// every node gets ceil(parts/nodes) or one less, whatever the order the
// nodes are listed in.
func TestAssignIsFairAndDeterministic(t *testing.T) {
	for _, nodes := range [][]string{{"a"}, {"a", "b"}, {"core-1", "core-2", "core-3"}, {"a", "b", "c", "d"}, {"a", "b", "c", "d", "e"}} {
		got := assign(nodes, events.Partitions)
		rev := slices.Clone(nodes)
		slices.Reverse(rev)
		if !slices.Equal(got, assign(append(rev, rev[0]), events.Partitions)) {
			t.Errorf("%v: the assignment depends on the order or repeats of the list", nodes)
		}
		limit := (events.Partitions + len(nodes) - 1) / len(nodes)
		total := 0
		for _, n := range nodes {
			l := loads(got)[n]
			total += l
			if l > limit || l < limit-1 {
				t.Errorf("%v: %s owns %d partitions, want %d or %d", nodes, n, l, limit-1, limit)
			}
		}
		if total != events.Partitions {
			t.Errorf("%v: %d partitions assigned, want %d", nodes, total, events.Partitions)
		}
	}
}

// a node that leaves gives up only its own partitions; a node that comes
// back takes about its share and the others keep the rest.
func TestAssignMovesFewPartitions(t *testing.T) {
	three := assign([]string{"core-1", "core-2", "core-3"}, events.Partitions)
	two := assign([]string{"core-1", "core-2"}, events.Partitions)
	extra := 0
	for p := range events.Partitions {
		if three[p] != "core-3" && two[p] != three[p] {
			extra++
		}
	}
	t.Logf("a node left: %d partitions of the remaining nodes moved besides its own", extra)
	if extra > 4 {
		t.Errorf("%d partitions of the remaining nodes moved when one left", extra)
	}
	moved := 0
	back := assign([]string{"core-1", "core-2", "core-3"}, events.Partitions)
	for p := range events.Partitions {
		if back[p] != two[p] {
			moved++
		}
	}
	// the returning node takes its 10 or 11; a few more may move when a
	// remaining node gives up a partition to stay within its share
	t.Logf("a node joined: %d partitions moved", moved)
	if moved > 15 {
		t.Errorf("%d partitions moved when a third node joined", moved)
	}
}

// members of the first list are settled at once, later ones only after
// memberSettle; a member that is no longer listed is out at once.
func TestMembershipSettles(t *testing.T) {
	m := newMembership(nil, "core-1", "core-1-x", obs.NewLogger(testLogWriter{t}, slog.LevelInfo))
	now := time.Now()
	m.update([]string{"core-1", "core-2"}, now)
	if got := loads(m.owners); got["core-1"] != 16 || got["core-2"] != 16 {
		t.Fatalf("first list: %v", got)
	}
	m.update([]string{"core-1", "core-2", "core-3"}, now.Add(time.Second))
	if got := loads(m.owners); got["core-3"] != 0 {
		t.Fatalf("a new member got partitions before it settled: %v", got)
	}
	m.update([]string{"core-1", "core-2", "core-3"}, now.Add(time.Second+memberSettle))
	if got := loads(m.owners); got["core-3"] < 10 {
		t.Fatalf("a settled member has %d partitions", got["core-3"])
	}
	m.update([]string{"core-1"}, now.Add(time.Minute))
	if got := loads(m.owners); got["core-1"] != events.Partitions {
		t.Fatalf("members that left still own partitions: %v", got)
	}
	// this instance counts even before its own key is listed
	m2 := newMembership(nil, "core-9", "core-9-x", obs.NewLogger(testLogWriter{t}, slog.LevelInfo))
	m2.update([]string{"core-1"}, now)
	if got := loads(m2.owners); got["core-9"] != 16 {
		t.Fatalf("own node missing from the assignment: %v", got)
	}
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

// the barrier holds a sequence back until a read clears it, and a read that
// finds nothing of the other kind clears everything fetched before it.
func TestSeqBarrierBookkeeping(t *testing.T) {
	b := newSeqBarrier(nil, "", false)
	if b.clear(5) {
		t.Fatal("5 clear before any read")
	}
	b.fetched(9)
	b.fetched(4)
	if b.covered.Load() != 9 {
		t.Fatalf("covered %d", b.covered.Load())
	}
	// as a read that found the other kind's oldest at 7
	b.raise(7)
	if !b.clear(6) || b.clear(7) {
		t.Fatal("raise(7) must clear 6 and not 7")
	}
	b.raise(3)
	if b.below.Load() != 7 {
		t.Fatal("the barrier moved back")
	}
	b.retired.Store(true)
	if !b.clear(1 << 40) {
		t.Fatal("a retired barrier holds a message back")
	}
}
