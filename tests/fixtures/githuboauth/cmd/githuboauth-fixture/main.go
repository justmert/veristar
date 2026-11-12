// TEST-ONLY: runs the github oauth fixture (tests/fixtures/githuboauth) as a
// process for the browser suites. it is never deployed; only control plane
// binaries built with the oauthfixture tag can be pointed at it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/justmert/stellar-analytics/tests/fixtures/githuboauth"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "address to listen on")
	clientID := flag.String("client-id", os.Getenv("FIXTURE_CLIENT_ID"), "registered client id")
	clientSecret := flag.String("client-secret", os.Getenv("FIXTURE_CLIENT_SECRET"), "registered client secret")
	redirect := flag.String("redirect-url", os.Getenv("FIXTURE_REDIRECT_URL"), "registered callback url")
	flag.Parse()
	if *clientID == "" || *clientSecret == "" || *redirect == "" {
		fmt.Fprintln(os.Stderr, "githuboauth-fixture: -client-id, -client-secret and -redirect-url are required")
		os.Exit(2)
	}
	s := githuboauth.New(githuboauth.Config{ClientID: *clientID, ClientSecret: *clientSecret, RedirectURL: *redirect})
	srv := &http.Server{Addr: *listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("TEST-ONLY github oauth fixture listening on %s", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
