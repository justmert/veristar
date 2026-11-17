package rotationcheck

import (
	"bufio"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeNATS answers the nats handshake on loopback the way answer says:
// "refuse" sends the server's authorization violation, "accept" the pong
// of a good login, "reset" drops the connection with a tcp reset after
// the client's CONNECT. answer gets the 1-based count of connections.
func fakeNATS(t *testing.T, answer func(n int64) string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var conns atomic.Int64
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n := conns.Add(1)
			go func() {
				defer c.Close()
				_, _ = c.Write([]byte(`INFO {"server_id":"fake","version":"2.15.3","proto":1,"max_payload":1048576,"auth_required":true}` + "\r\n"))
				r := bufio.NewReader(c)
				line, err := r.ReadString('\n')
				if err != nil || !strings.HasPrefix(line, "CONNECT ") {
					return
				}
				switch answer(n) {
				case "refuse":
					_, _ = c.Write([]byte("-ERR 'Authorization Violation'\r\n"))
				case "accept":
					if _, err := r.ReadString('\n'); err == nil {
						_, _ = c.Write([]byte("PONG\r\n"))
						_, _ = r.ReadString('\n')
					}
				case "reset":
					_ = c.(*net.TCPConn).SetLinger(0)
				}
			}()
		}
	}()
	return "nats://" + l.Addr().String()
}

func always(a string) func(int64) string { return func(int64) string { return a } }

func TestNATSAttemptAsksEveryServer(t *testing.T) {
	refuse := fakeNATS(t, always("refuse"))
	cases := []struct {
		name string
		urls []string
		want Outcome
	}{
		// a reset from the last server must not hide that the others said
		// the credential is wrong, once a retry gets that server's answer
		{"refused, then a reset that clears", []string{refuse, refuse, fakeNATS(t, func(n int64) string {
			if n == 1 {
				return "reset"
			}
			return "refuse"
		})}, Refused},
		// a server that never answers keeps the result inconclusive: a
		// refusal counts only when every server said so
		{"refused, then a server that always resets", []string{refuse, fakeNATS(t, always("reset"))}, Inconclusive},
		// one server that still takes the credential is enough to fail a
		// rotation, wherever it is in the list
		{"refused, then accepted", []string{refuse, fakeNATS(t, always("accept"))}, Accepted},
		{"accepted, then refused", []string{fakeNATS(t, always("accept")), refuse}, Accepted},
		{"refused everywhere", []string{refuse, refuse}, Refused},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, detail := natsAttempt(c.urls, nil, "u", "p"); got != c.want {
				t.Fatalf("got %s (%s), want %s", got, detail, c.want)
			}
		})
	}
}
