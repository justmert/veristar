package reports

import (
	"fmt"
	"net/http"
	"strings"
)

// StellarTOMLPath is where sep-1 places a domain's stellar.toml.
const StellarTOMLPath = "/.well-known/stellar.toml"

// sep1Version is the version of sep-1 the file follows.
const sep1Version = "2.7.0"

// publicPassphrase is the network the report keys belong to; they hold no
// funds and sign no transaction, but sep-1 names a network per file.
const publicPassphrase = "Public Global Stellar Network ; September 2015"

// StellarTOML is the console host's stellar.toml: ACCOUNTS lists the
// current report key and every retired one, the accounts this domain
// controls (sep-1). SIGNING_KEY is left out on purpose: sep-1 reserves it
// for sep-10 and sep-45 authentication servers.
func (h *Routes) StellarTOML() string {
	k := h.Keys()
	accounts := append([]string{k.Current}, k.Retired...)
	quoted := make([]string, len(accounts))
	for i, a := range accounts {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	var b strings.Builder
	b.WriteString("# the accounts below are veristar's usage report signing keys: the first is\n")
	b.WriteString("# the current key, the others signed earlier reports. they hold no funds.\n")
	b.WriteString("# verify a report: " + h.c.ConsoleOrigin + "/r/<report id>\n")
	fmt.Fprintf(&b, "VERSION = %q\n", sep1Version)
	fmt.Fprintf(&b, "NETWORK_PASSPHRASE = %q\n", publicPassphrase)
	fmt.Fprintf(&b, "ACCOUNTS = [%s]\n", strings.Join(quoted, ", "))
	return b.String()
}

// StellarTOMLHandler serves StellarTOML at StellarTOMLPath with the cors
// header sep-1 requires and a text/plain type, so browsers show it.
func (h *Routes) StellarTOMLHandler() http.Handler {
	body := []byte(h.StellarTOML())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != StellarTOMLPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
