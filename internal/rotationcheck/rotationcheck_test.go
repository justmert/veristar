package rotationcheck

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestPairOutcomes(t *testing.T) {
	same := pair("postgres", "x", func(string) (Outcome, string) { return Refused, "" }, Pair{Old: "a", New: "a"})
	if same.OK() {
		t.Fatal("an unchanged value passed")
	}
	ok := pair("nats", "x", func(pw string) (Outcome, string) {
		if pw == "new" {
			return Accepted, ""
		}
		return Refused, ""
	}, Pair{Old: "old", New: "new"})
	if !ok.OK() {
		t.Fatalf("rotated pair not ok: %s", ok)
	}
}

// a server that does not answer is inconclusive, never refused
func TestUnreachableIsInconclusive(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg := &Postgres{Host: "127.0.0.1", Port: addr.Port, SSLMode: "disable"}
	if o, _ := pgAttempt(ctx, pg, "u", "p", "d", false); o != Inconclusive {
		t.Fatalf("postgres: %s", o)
	}
	if o, _ := natsAttempt([]string{"nats://" + addr.String()}, nil, "u", "p"); o != Inconclusive {
		t.Fatalf("nats: %s", o)
	}
}
