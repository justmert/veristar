// Command receiver is a TEST FIXTURE, not a discord stand-in for production:
// a local webhook endpoint for the this stage fire-drills. alertmanager's
// discord_configs and gatus's discord provider post to it exactly as they
// would post to a discord webhook an operator input is not available), and it keeps
// every body so a drill can show that an alert arrived and resolved.
//
//	POST /webhook/<source>  stores the json body, answers 204 like discord
//	GET  /messages          every stored message as a json array
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type message struct {
	At     time.Time       `json:"at"`
	Source string          `json:"source"`
	Body   json.RawMessage `json:"body"`
}

func main() {
	listen := flag.String("listen", "0.0.0.0:8099", "listen address")
	flag.Parse()
	var mu sync.Mutex
	var msgs []message
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !json.Valid(b) {
			http.Error(w, "json body required", http.StatusBadRequest)
			return
		}
		m := message{At: time.Now().UTC(), Source: strings.TrimPrefix(r.URL.Path, "/webhook/"), Body: b}
		mu.Lock()
		msgs = append(msgs, m)
		mu.Unlock()
		log.Printf("received from %s: %s", m.Source, b)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /messages", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(msgs)
	})
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
