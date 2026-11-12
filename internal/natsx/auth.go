package natsx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// RenderAuthorization renders users as a nats server `authorization` block.
// credentials[name] is the password entry for each user: a bcrypt hash in
// production (nats-server accepts hashes that start with $2a$), any string in
// tests. every user must have exactly one entry, so a service cannot be left
// out or an unknown user slip in.
func RenderAuthorization(users []User, credentials map[string]string) ([]byte, error) {
	type perm struct {
		Allow []string `json:"allow"`
	}
	type permissions struct {
		Publish        perm `json:"publish"`
		Subscribe      perm `json:"subscribe"`
		AllowResponses bool `json:"allow_responses,omitempty"`
	}
	type entry struct {
		User        string      `json:"user"`
		Password    string      `json:"password"`
		Permissions permissions `json:"permissions"`
	}
	seen := map[string]bool{}
	var entries []entry
	for _, u := range users {
		pw, ok := credentials[u.Name]
		if !ok || pw == "" {
			return nil, fmt.Errorf("natsx: no credential for nats user %s", u.Name)
		}
		if seen[u.Name] {
			return nil, fmt.Errorf("natsx: nats user %s declared twice", u.Name)
		}
		seen[u.Name] = true
		entries = append(entries, entry{
			User:     u.Name,
			Password: pw,
			Permissions: permissions{
				Publish:        perm{Allow: u.Publish},
				Subscribe:      perm{Allow: u.Subscribe},
				AllowResponses: u.AllowResponses,
			},
		})
	}
	var extra []string
	for name := range credentials {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		slices.Sort(extra)
		return nil, fmt.Errorf("natsx: credentials for unknown nats users %s", strings.Join(extra, ", "))
	}
	// the nats configuration format accepts json values, and json quoting
	// keeps a $ in a bcrypt hash from being read as a variable reference.
	// the nats parser knows no \u escapes, so html escaping (which turns > of
	// a wildcard into \u003e) is off and control characters are refused.
	var buf bytes.Buffer
	buf.WriteString("authorization: ")
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"users": entries}); err != nil {
		return nil, err
	}
	if bytes.Contains(buf.Bytes(), []byte(`\u`)) {
		return nil, errors.New("natsx: a user name, subject or credential contains a control character")
	}
	return buf.Bytes(), nil
}
