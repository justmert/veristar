//go:build otelapps

package otelapps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// the load plan is fixed so every run knows exactly how many requests each
// route and status must show in both backends. counts are keyed by the
// canonical route template, which routeFor maps to each framework's syntax.
const (
	accountsCreated   = 20
	badAccounts       = 5
	unknownLookups    = 7
	paymentsPerAcct   = 2
	badPayments       = 6
	paymentsNoAcct    = 3
	unknownPayLists   = 4
	routeAccounts     = "/accounts"
	routeAccount      = "/accounts/{id}"
	routeAccountPays  = "/accounts/{id}/payments"
	unknownAccountIDs = 900000
)

// expectedCounts is what driveLoad sends, by canonical route.
func expectedCounts() map[requestKey]int64 {
	return map[requestKey]int64{
		{"POST", routeAccounts, 201}:    accountsCreated,
		{"POST", routeAccounts, 400}:    badAccounts,
		{"GET", routeAccount, 200}:      accountsCreated,
		{"GET", routeAccount, 404}:      unknownLookups,
		{"POST", routeAccountPays, 201}: accountsCreated * paymentsPerAcct,
		{"POST", routeAccountPays, 400}: badPayments,
		{"POST", routeAccountPays, 404}: paymentsNoAcct,
		{"GET", routeAccountPays, 200}:  accountsCreated,
		{"GET", routeAccountPays, 404}:  unknownPayLists,
	}
}

func totalRequests() int64 {
	var n int64
	for _, v := range expectedCounts() {
		n += v
	}
	return n
}

type driver struct {
	base   string
	client *http.Client
	sent   map[requestKey]int64
}

// do sends one request and fails unless the app answered with want. the
// driver counts by what it asked for, so a wrong status is a test failure,
// not a silently different count.
func (d *driver) do(method, path, route string, body any, want int) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != want {
		return nil, fmt.Errorf("%s %s: status %d, want %d: %s", method, path, res.StatusCode, want, strings.TrimSpace(string(out)))
	}
	d.sent[requestKey{method, route, want}]++
	return out, nil
}

// driveLoad runs the fixed plan against base and returns what it sent.
// midpoint runs between the lookups and the payments, so a pause there makes
// the sdk export periodically while traffic is still flowing.
func driveLoad(base string, midpoint func()) (map[requestKey]int64, error) {
	d := &driver{base: base, client: &http.Client{Timeout: 10 * time.Second}, sent: map[requestKey]int64{}}
	var ids []string
	for i := 0; i < accountsCreated; i++ {
		b, err := d.do("POST", "/accounts", routeAccounts, map[string]string{"name": fmt.Sprintf("account %d", i)}, 201)
		if err != nil {
			return nil, err
		}
		var a struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(b, &a); err != nil || a.ID == "" {
			return nil, fmt.Errorf("create account answered %s", b)
		}
		ids = append(ids, a.ID)
	}
	for i := 0; i < badAccounts; i++ {
		if _, err := d.do("POST", "/accounts", routeAccounts, map[string]string{"name": ""}, 400); err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		b, err := d.do("GET", "/accounts/"+id, routeAccount, nil, 200)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(b, []byte(`"`+id+`"`)) {
			return nil, fmt.Errorf("account %s answered %s", id, b)
		}
	}
	for i := 0; i < unknownLookups; i++ {
		if _, err := d.do("GET", fmt.Sprintf("/accounts/%d", unknownAccountIDs+i), routeAccount, nil, 404); err != nil {
			return nil, err
		}
	}
	midpoint()
	for _, id := range ids {
		for p := 0; p < paymentsPerAcct; p++ {
			amount := fmt.Sprintf("%d.%07d", p+1, 1234567)
			if _, err := d.do("POST", "/accounts/"+id+"/payments", routeAccountPays, map[string]string{"amount": amount}, 201); err != nil {
				return nil, err
			}
		}
	}
	badAmounts := []string{"", "abc", "-1", "0", "1e", "--5"}
	for i := 0; i < badPayments; i++ {
		if _, err := d.do("POST", "/accounts/"+ids[i]+"/payments", routeAccountPays, map[string]string{"amount": badAmounts[i%len(badAmounts)]}, 400); err != nil {
			return nil, err
		}
	}
	for i := 0; i < paymentsNoAcct; i++ {
		if _, err := d.do("POST", fmt.Sprintf("/accounts/%d/payments", unknownAccountIDs+i), routeAccountPays, map[string]string{"amount": "1"}, 404); err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		b, err := d.do("GET", "/accounts/"+id+"/payments", routeAccountPays, nil, 200)
		if err != nil {
			return nil, err
		}
		var l struct {
			Payments []json.RawMessage `json:"payments"`
		}
		if err := json.Unmarshal(b, &l); err != nil {
			return nil, fmt.Errorf("payments of %s answered %s", id, b)
		}
		if len(l.Payments) != paymentsPerAcct {
			return nil, fmt.Errorf("payments of %s: %d, want %d (rejected payments must not be stored)", id, len(l.Payments), paymentsPerAcct)
		}
	}
	for i := 0; i < unknownPayLists; i++ {
		if _, err := d.do("GET", fmt.Sprintf("/accounts/%d/payments", unknownAccountIDs+i), routeAccountPays, nil, 404); err != nil {
			return nil, err
		}
	}
	return d.sent, nil
}
