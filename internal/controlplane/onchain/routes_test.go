package onchain_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/require"

	"github.com/justmert/veristar/internal/chain"
	"github.com/justmert/veristar/internal/controlplane"
	"github.com/justmert/veristar/internal/controlplane/exports"
	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/onchain"
	"github.com/justmert/veristar/internal/controlplane/session"
	"github.com/justmert/veristar/internal/controlplane/tenants"
	pdb "github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/jobs/jobstest"
)

// ledger is the fake horizon and rpc the proofs read: account records with
// data entries, transactions by hash and contract instances.
type ledger struct {
	mu        sync.Mutex
	data      map[string]map[string]string
	txs       map[string]string
	instances map[string]string
	// signers and thresholds of an account; without an entry the master
	// key weighs 1 and every threshold is 0, a fresh account
	signers    map[string][]map[string]any
	thresholds map[string]map[string]int
	// onAccount runs before an account record is served
	onAccount func(id string)
	// onRPC runs before an rpc request is answered
	onRPC func()
}

func (l *ledger) horizon(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/accounts/") && l.onAccount != nil {
		l.onAccount(strings.TrimPrefix(r.URL.Path, "/accounts/"))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/accounts/"):
		id := strings.TrimPrefix(r.URL.Path, "/accounts/")
		d, ok := l.data[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		enc := map[string]string{}
		for k, v := range d {
			enc[k] = base64.StdEncoding.EncodeToString([]byte(v))
		}
		signers, ok := l.signers[id]
		if !ok {
			signers = []map[string]any{{"key": id, "weight": 1, "type": "ed25519_public_key"}}
		}
		th, ok := l.thresholds[id]
		if !ok {
			th = map[string]int{"low_threshold": 0, "med_threshold": 0, "high_threshold": 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"account_id": id, "data": enc, "signers": signers, "thresholds": th})
	case strings.HasPrefix(r.URL.Path, "/transactions/"):
		h := strings.TrimPrefix(r.URL.Path, "/transactions/")
		env, ok := l.txs[h]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hash": h, "successful": true, "envelope_xdr": env})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (l *ledger) rpc(w http.ResponseWriter, r *http.Request) {
	if l.onRPC != nil {
		l.onRPC()
	}
	var req struct {
		ID     int64          `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	l.mu.Lock()
	defer l.mu.Unlock()
	entries := []map[string]any{}
	if req.Method == "getLedgerEntries" {
		for _, k := range req.Params["keys"].([]any) {
			for c, entry := range l.instances {
				key, _ := chain.InstanceKey(c)
				if key == k.(string) {
					entries = append(entries, map[string]any{"key": key, "xdr": entry, "lastModifiedLedgerSeq": 1})
				}
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"entries": entries, "latestLedger": 10}})
}

type env struct {
	d    *pdb.DB
	srv  *httptest.Server
	l    *ledger
	ghID int64
}

func start(t *testing.T) *env {
	t.Helper()
	pg := jobstest.Postgres(t)
	pcfg, err := pdb.LoadPoolConfig(pg.Config("platform"), pdb.RoleCPSystem, pdb.RoleCPTenant, pdb.RoleJobs)
	require.NoError(t, err)
	d, err := pdb.Open(t.Context(), pcfg, "onchain-test")
	require.NoError(t, err)
	t.Cleanup(d.Close)
	log := slog.New(slog.DiscardHandler)
	scope, err := tenants.NewScope(d, "test-terms", tenants.ReconcileFunc(func(context.Context, uuid.UUID) error {
		return errors.New("onchain routes must not reconcile")
	}), log)
	require.NoError(t, err)

	l := &ledger{data: map[string]map[string]string{}, txs: map[string]string{}, instances: map[string]string{},
		signers: map[string][]map[string]any{}, thresholds: map[string]map[string]int{}}
	hz := httptest.NewServer(http.HandlerFunc(l.horizon))
	t.Cleanup(hz.Close)
	rp := httptest.NewServer(http.HandlerFunc(l.rpc))
	t.Cleanup(rp.Close)
	he, err := chain.NewEndpoint(hz.URL, 1000, "test")
	require.NoError(t, err)
	re, err := chain.NewEndpoint(rp.URL, 1000, "test")
	require.NoError(t, err)
	routes, err := onchain.NewRoutes(scope, chain.NewHorizon(he), chain.NewRPC(re), "https://console.example.org", time.Now)
	require.NoError(t, err)
	overview, err := exports.NewOverview(scope, time.Now)
	require.NoError(t, err)

	sessions := session.New(d)
	e := &env{d: d, l: l, ghID: 9_400_000}
	e.srv = httptest.NewUnstartedServer(http.NotFoundHandler())
	e.srv.StartTLS()
	t.Cleanup(e.srv.Close)
	signIn := controlplane.RoutesFunc(func(r chi.Router) {
		r.Method(http.MethodGet, "/test-sign-in", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			id, err := uuid.Parse(r.URL.Query().Get("user"))
			if err != nil {
				return httpx.NotFound()
			}
			if err := sessions.SignIn(r.Context(), id); err != nil {
				return err
			}
			w.WriteHeader(http.StatusNoContent)
			return nil
		}))
	})
	e.srv.Config.Handler = controlplane.NewHandler(controlplane.Options{
		Log: log, ConsoleOrigin: e.srv.URL, Sessions: sessions,
		Public:  []controlplane.Routes{signIn},
		Private: []controlplane.Routes{routes, overview},
	})
	return e
}

func (e *env) sys(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	require.NoError(t, e.d.WithSystem(t.Context(), fn))
}

func (e *env) jobs(t *testing.T, fn func(pgx.Tx) error) {
	t.Helper()
	require.NoError(t, e.d.WithRole(t.Context(), pdb.RoleJobs, fn))
}

func (e *env) user(t *testing.T) uuid.UUID {
	t.Helper()
	e.ghID++
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `insert into cp.users (github_id, github_login) values ($1, $2) returning id`,
			e.ghID, "u"+strconv.FormatInt(e.ghID, 10)).Scan(&id)
	})
	return id
}

func (e *env) tenant(t *testing.T, owner uuid.UUID, enrolled []string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	e.sys(t, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(),
			`insert into cp.tenants (slug, name, status, created_by, accepted_terms_version, enrollment_answers)
			 values ($1, 'Project', 'active', $2, 'test-terms', $3) returning id`,
			"p-"+uuid.NewString()[:8], owner, map[string]any{"stellar_ids": enrolled}).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'owner')`, id, owner)
		return err
	})
	return id
}

type client struct {
	e  *env
	hc *http.Client
}

func (e *env) client(t *testing.T, user uuid.UUID) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	hc := *e.srv.Client()
	hc.Jar = jar
	res, err := hc.Get(e.srv.URL + "/api/test-sign-in?user=" + user.String())
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	return &client{e: e, hc: &hc}
}

type resp struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

func (c *client) call(t *testing.T, method, path string, body any) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, c.e.srv.URL+path, rd)
	require.NoError(t, err)
	req.Header.Set("X-Requested-With", "sa-console")
	req.Header.Set("Origin", c.e.srv.URL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	out := resp{status: res.StatusCode, header: res.Header, raw: raw}
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &out.body), string(raw))
	}
	return out
}

func field(r resp, name string) string {
	e, _ := r.body["error"].(map[string]any)
	f, _ := e["fields"].(map[string]any)
	s, _ := f[name].(string)
	return s
}

func contractID(t *testing.T, deployer string, salt byte) (string, xdr.ScAddress, xdr.Uint256) {
	t.Helper()
	var id xdr.AccountId
	require.NoError(t, id.SetAddress(deployer))
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id}
	var s xdr.Uint256
	s[0] = salt
	c, err := chain.ContractIDFromAddress(chain.PublicNetwork, addr, s)
	require.NoError(t, err)
	return c, addr, s
}

func deployEnvelope(t *testing.T, deployer string, addr xdr.ScAddress, salt xdr.Uint256) string {
	t.Helper()
	var src xdr.MuxedAccount
	require.NoError(t, src.SetAddress(deployer))
	wasm := xdr.Hash{9}
	env := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: &xdr.TransactionV1Envelope{Tx: xdr.Transaction{
		SourceAccount: src, Fee: 100, SeqNum: 1, Cond: xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Operations: []xdr.Operation{{Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{HostFunction: xdr.HostFunction{
				Type: xdr.HostFunctionTypeHostFunctionTypeCreateContract,
				CreateContract: &xdr.CreateContractArgs{
					ContractIdPreimage: xdr.ContractIdPreimage{
						Type:        xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
						FromAddress: &xdr.ContractIdPreimageFromAddress{Address: addr, Salt: salt},
					},
					Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &wasm},
				},
			}},
		}}},
	}}}
	b, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	return b
}

func instanceWithAdmin(t *testing.T, contract string, admin string) string {
	t.Helper()
	key, err := chain.InstanceKey(contract)
	require.NoError(t, err)
	var lk xdr.LedgerKey
	require.NoError(t, xdr.SafeUnmarshalBase64(key, &lk))
	var id xdr.AccountId
	require.NoError(t, id.SetAddress(admin))
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id}
	sym := xdr.ScSymbol("Admin")
	wasm := xdr.Hash{3}
	storage := xdr.ScMap{{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}, Val: xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}}}
	data := xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.ContractDataEntry{
		Contract: lk.ContractData.Contract, Key: lk.ContractData.Key, Durability: xdr.ContractDataDurabilityPersistent,
		Val: xdr.ScVal{Type: xdr.ScValTypeScvContractInstance, Instance: &xdr.ScContractInstance{
			Executable: xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &wasm},
			Storage:    &storage,
		}},
	}}
	b, err := xdr.MarshalBase64(data)
	require.NoError(t, err)
	return b
}

func idOf(t *testing.T, view resp, strkey string) map[string]any {
	t.Helper()
	for _, x := range view.body["ids"].([]any) {
		m := x.(map[string]any)
		if m["strkey"] == strkey {
			return m
		}
	}
	t.Fatalf("%s is not in the view", strkey)
	return nil
}

func TestOnchainRoutes(t *testing.T) {
	e := start(t)
	owner := e.user(t)
	acctKP := keypair.MustRandom()
	account := acctKP.Address()
	second := keypair.MustRandom()
	tenant := e.tenant(t, owner, []string{account})
	c := e.client(t, owner)
	base := "/api/tenants/" + tenant.String() + "/onchain"

	// the enrollment id is there, unverified
	v := c.call(t, http.MethodGet, base, nil)
	require.Equal(t, http.StatusOK, v.status, string(v.raw))
	acct := idOf(t, v, account)
	require.Equal(t, "enrollment", acct["added_by"])
	require.Equal(t, "unverified", acct["verification"].(map[string]any)["status"])
	series := acct["series"].([]any)
	require.Len(t, series, 30)
	last := series[29].(map[string]any)
	require.Nil(t, last["operations"])
	require.Equal(t, onchain.MissingNotComplete, last["missing"])
	require.Equal(t, onchain.MissingNotReadYet, series[0].(map[string]any)["missing"])

	// add: a second account and two contracts; refusals
	r := c.call(t, http.MethodPost, base+"/ids", map[string]any{"strkey": second.Address()})
	require.Equal(t, http.StatusCreated, r.status, string(r.raw))
	secondID := r.body["id"].(string)
	adminContract, _, _ := contractID(t, account, 1)
	deployContract, depAddr, depSalt := contractID(t, account, 2)
	for _, s := range []string{adminContract, deployContract} {
		r = c.call(t, http.MethodPost, base+"/ids", map[string]any{"strkey": s})
		require.Equal(t, http.StatusCreated, r.status, string(r.raw))
		require.Equal(t, "contract", r.body["kind"])
	}
	r = c.call(t, http.MethodPost, base+"/ids", map[string]any{"strkey": second.Address()})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	require.Equal(t, httpx.FieldDuplicate, field(r, "strkey"))
	r = c.call(t, http.MethodPost, base+"/ids", map[string]any{"strkey": "MA7QYNF7SOWQ3GLR2BGMZEHXAVIRZA4KVWLTJJFC7MGXUA74P7UJVAGZB"})
	require.Equal(t, httpx.FieldInvalidStrkey, field(r, "strkey"))

	accountID := acct["id"].(string)
	// sep-53: a challenge, a wrong signature, then the right one
	ch := c.call(t, http.MethodPost, base+"/ids/"+accountID+"/challenge", nil)
	require.Equal(t, http.StatusOK, ch.status, string(ch.raw))
	msg := ch.body["message"].(string)
	require.Contains(t, msg, "Veristar ownership proof\nConsole: console.example.org\nProject: \"Project\" ("+tenant.String()+")\nAccount: "+account)
	wrong := keypair.MustRandom()
	h := chain.SEP53Hash(msg)
	bad, err := wrong.Sign(h[:])
	require.NoError(t, err)
	r = c.call(t, http.MethodPost, base+"/ids/"+accountID+"/verify", map[string]any{"method": "sep53", "signature": base64.StdEncoding.EncodeToString(bad)})
	require.Equal(t, http.StatusOK, r.status, string(r.raw))
	ver := r.body["verification"].(map[string]any)
	require.Equal(t, "unverified", ver["status"])
	require.Equal(t, onchain.ErrBadSignature, ver["last_error"])
	good, err := acctKP.Sign(h[:])
	require.NoError(t, err)
	// the signature holds, but the account is read from the ledger first:
	// an account that does not exist proves nothing
	r = c.call(t, http.MethodPost, base+"/ids/"+accountID+"/verify", map[string]any{"method": "sep53", "signature": base64.StdEncoding.EncodeToString(good)})
	require.Equal(t, chain.CodeAccountMissing, r.body["verification"].(map[string]any)["last_error"], string(r.raw))
	e.l.mu.Lock()
	e.l.data[account] = map[string]string{}
	e.l.mu.Unlock()
	r = c.call(t, http.MethodPost, base+"/ids/"+accountID+"/verify", map[string]any{"method": "sep53", "signature": base64.StdEncoding.EncodeToString(good)})
	ver = r.body["verification"].(map[string]any)
	require.Equal(t, "verified", ver["status"], string(r.raw))
	require.Equal(t, "sep53", ver["method"])
	require.Nil(t, ver["last_error"])
	// the challenge is spent: the same signature again has no challenge
	r = c.call(t, http.MethodPost, base+"/ids/"+accountID+"/verify", map[string]any{"method": "sep53", "signature": base64.StdEncoding.EncodeToString(good)})
	ver = r.body["verification"].(map[string]any)
	require.Equal(t, "verified", ver["status"], "a failed attempt keeps the earlier proof")
	require.Equal(t, onchain.ErrNoChallenge, ver["last_error"])

	// manage data: the account publishes the nonce as data entry veristar
	ch = c.call(t, http.MethodPost, base+"/ids/"+secondID+"/challenge", nil)
	md := ch.body["manage_data"].(map[string]any)
	require.Equal(t, "veristar", md["name"])
	e.l.mu.Lock()
	e.l.data[second.Address()] = map[string]string{"veristar": "not-the-nonce"}
	e.l.mu.Unlock()
	r = c.call(t, http.MethodPost, base+"/ids/"+secondID+"/verify", map[string]any{"method": "manage_data"})
	require.Equal(t, onchain.ErrDataMismatch, r.body["verification"].(map[string]any)["last_error"])
	e.l.mu.Lock()
	e.l.data[second.Address()] = map[string]string{"veristar": md["value"].(string)}
	e.l.mu.Unlock()
	r = c.call(t, http.MethodPost, base+"/ids/"+secondID+"/verify", map[string]any{"method": "manage_data"})
	require.Equal(t, "verified", r.body["verification"].(map[string]any)["status"], string(r.raw))

	// a contract method on an account, and a challenge for a contract, are refused
	r = c.call(t, http.MethodPost, base+"/ids/"+secondID+"/verify", map[string]any{"method": "contract_admin"})
	require.Equal(t, http.StatusUnprocessableEntity, r.status)
	v = c.call(t, http.MethodGet, base, nil)
	adminID := idOf(t, v, adminContract)["id"].(string)
	deployID := idOf(t, v, deployContract)["id"].(string)
	r = c.call(t, http.MethodPost, base+"/ids/"+adminID+"/challenge", nil)
	require.Equal(t, http.StatusUnprocessableEntity, r.status)

	// contract admin: the instance names a key that is not the project's,
	// then the verified account
	e.l.mu.Lock()
	e.l.instances[adminContract] = instanceWithAdmin(t, adminContract, keypair.MustRandom().Address())
	e.l.mu.Unlock()
	r = c.call(t, http.MethodPost, base+"/ids/"+adminID+"/verify", map[string]any{"method": "contract_admin"})
	require.Equal(t, onchain.ErrAdminNotVerified, r.body["verification"].(map[string]any)["last_error"], string(r.raw))
	e.l.mu.Lock()
	e.l.instances[adminContract] = instanceWithAdmin(t, adminContract, account)
	e.l.mu.Unlock()
	r = c.call(t, http.MethodPost, base+"/ids/"+adminID+"/verify", map[string]any{"method": "contract_admin"})
	ver = r.body["verification"].(map[string]any)
	require.Equal(t, "verified", ver["status"], string(r.raw))
	require.Equal(t, account, ver["via"])
	require.Equal(t, "Admin", ver["detail"].(map[string]any)["storage_key"])

	// contract deployer: the deploying transaction recomputes the id
	hash := strings.Repeat("ab", 32)
	e.l.mu.Lock()
	e.l.txs[hash] = deployEnvelope(t, account, depAddr, depSalt)
	e.l.mu.Unlock()
	r = c.call(t, http.MethodPost, base+"/ids/"+deployID+"/verify", map[string]any{"method": "contract_deployer", "tx_hash": strings.Repeat("cd", 32)})
	require.Equal(t, chain.CodeNotFound, r.body["verification"].(map[string]any)["last_error"])
	r = c.call(t, http.MethodPost, base+"/ids/"+deployID+"/verify", map[string]any{"method": "contract_deployer", "tx_hash": hash})
	ver = r.body["verification"].(map[string]any)
	require.Equal(t, "verified", ver["status"], string(r.raw))
	require.Equal(t, hash, ver["detail"].(map[string]any)["tx_hash"])
	r = c.call(t, http.MethodPost, base+"/ids/"+deployID+"/verify", map[string]any{"method": "contract_deployer", "tx_hash": "xyz"})
	require.Equal(t, httpx.FieldInvalidFormat, field(r, "tx_hash"))

	// activity: rows stored by chain-activity show, a failed read is missing
	today := time.Now().UTC().Truncate(24 * time.Hour)
	e.jobs(t, func(tx pgx.Tx) error {
		for i, n := range []int64{4, 0} {
			day := today.AddDate(0, 0, -(2 - i))
			if _, err := tx.Exec(t.Context(), `insert into agg.chain_daily (tenant_id, strkey, day, source, operations, transactions, counterparties)
				values ($1, $2, $3, 'horizon', $4, $5, $6)`, tenant, account, day, n, n/2, n/4); err != nil {
				return err
			}
		}
		_, err := tx.Exec(t.Context(), `insert into agg.chain_reads (tenant_id, strkey, covered_from, covered_to, last_attempt_at,
			last_success_at, home_domain, account_exists) values ($1, $2, $3, $4, now(), now(), 'alpha.example.org', true)`,
			tenant, account, today.AddDate(0, 0, -2), today.AddDate(0, 0, -1))
		if err != nil {
			return err
		}
		_, err = tx.Exec(t.Context(), `insert into agg.chain_reads (tenant_id, strkey, last_attempt_at, last_error, last_error_at)
			values ($1, $2, now(), 'rate_limited', now())`, tenant, second.Address())
		return err
	})
	v = c.call(t, http.MethodGet, base, nil)
	acct = idOf(t, v, account)
	series = acct["series"].([]any)
	require.EqualValues(t, 4, series[27].(map[string]any)["operations"])
	require.EqualValues(t, 0, series[28].(map[string]any)["operations"], "a stored zero is a zero")
	require.Equal(t, onchain.MissingBeforeBackfill, series[26].(map[string]any)["missing"])
	require.EqualValues(t, 4, acct["totals"].(map[string]any)["operations"])
	sec := idOf(t, v, second.Address())
	require.Equal(t, "rate_limited", sec["series"].([]any)[10].(map[string]any)["missing"])
	totals := v.body["totals"].(map[string]any)
	require.EqualValues(t, 4, totals["ids"])
	require.EqualValues(t, 4, totals["ids_verified"])

	// the two-way link: a verified site whose toml lists the account and
	// whose org url is on the domain, and the account's home domain
	site := uuid.Must(uuid.NewV7())
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `insert into cp.sites (id, tenant_id, name, domain, origins, quarantine_website_id, status,
			verify_token, verify_method, verified_at, last_verify_at, stellar_toml, stellar_toml_checked_at)
			values ($1, $2, 'Site', 'alpha.example.org', $3, $4, 'active', 'tok', 'dns', now(), now(), $5, now())`,
			site, tenant, []string{"https://alpha.example.org"}, uuid.Must(uuid.NewV7()), map[string]any{
				"status": "found", "org_url": "https://alpha.example.org", "org_url_same_domain": true,
				"accounts": []string{account}, "currencies": []map[string]any{{"contract": adminContract}},
			})
		return err
	})
	v = c.call(t, http.MethodGet, base, nil)
	link := v.body["link"].(map[string]any)
	require.Equal(t, true, link["two_way"], string(v.raw))
	ls := link["sites"].([]any)[0].(map[string]any)
	accts := ls["accounts"].([]any)
	require.Equal(t, map[string]any{"strkey": account, "listed_in_toml": true, "home_domain": "alpha.example.org",
		"home_domain_matches": true, "two_way": true}, accts[0])
	require.Equal(t, false, accts[1].(map[string]any)["two_way"])
	require.Contains(t, ls["contracts"], map[string]any{"strkey": adminContract, "listed_in_toml": true})

	// the overview export: one row per day, missing web days are empty
	r = c.call(t, http.MethodGet, "/api/tenants/"+tenant.String()+"/overview.csv?from="+today.AddDate(0, 0, -2).Format(time.DateOnly)+"&to="+today.Format(time.DateOnly), nil)
	require.Equal(t, http.StatusOK, r.status, string(r.raw))
	require.Equal(t, "text/csv; charset=utf-8", r.header.Get("Content-Type"))
	recs, err := csv.NewReader(bytes.NewReader(r.raw)).ReadAll()
	require.NoError(t, err)
	require.Equal(t, exports.OverviewHeader, recs[0])
	require.Len(t, recs, 4)
	// web columns, api requests, errors and infrastructure requests, then the
	// on-chain columns
	require.Equal(t, []string{today.AddDate(0, 0, -2).Format(time.DateOnly), "", "", "", "", "", "", "0", "0", "0", "0", "1", "4", "2"},
		recs[1][:14])
	var exportsAudited int
	e.sys(t, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `select count(*) from cp.audit_log where action = 'export.csv' and target = $1`,
			"tenant:"+tenant.String()).Scan(&exportsAudited)
	})
	require.Equal(t, 1, exportsAudited)

	// another project's member sees none of it
	other := e.user(t)
	e.tenant(t, other, nil)
	oc := e.client(t, other)
	for _, p := range []string{base, "/api/tenants/" + tenant.String() + "/overview.csv"} {
		r = oc.call(t, http.MethodGet, p, nil)
		require.Equal(t, http.StatusNotFound, r.status)
	}
	r = oc.call(t, http.MethodDelete, base+"/ids/"+secondID, nil)
	require.Equal(t, http.StatusNotFound, r.status)

	// a member who is not owner cannot remove; the owner can
	member := e.user(t)
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `insert into cp.tenant_members (tenant_id, user_id, role) values ($1, $2, 'member')`, tenant, member)
		return err
	})
	r = e.client(t, member).call(t, http.MethodDelete, base+"/ids/"+secondID, nil)
	require.Equal(t, http.StatusForbidden, r.status)
	r = c.call(t, http.MethodDelete, base+"/ids/"+secondID, nil)
	require.Equal(t, http.StatusNoContent, r.status)
	v = c.call(t, http.MethodGet, base, nil)
	require.Len(t, v.body["ids"], 3)

	// removing the account both contracts were proven through takes their
	// proofs with it: they rested on the account being the project's
	r = c.call(t, http.MethodDelete, base+"/ids/"+accountID, nil)
	require.Equal(t, http.StatusNoContent, r.status)
	v = c.call(t, http.MethodGet, base, nil)
	require.Len(t, v.body["ids"], 2)
	for _, s := range []string{adminContract, deployContract} {
		ver := idOf(t, v, s)["verification"].(map[string]any)
		require.Equal(t, "unverified", ver["status"], s)
		require.Equal(t, onchain.ErrViaRemoved, ver["last_error"], s)
		require.Nil(t, ver["via"], s)
	}
}

// a sep-53 signature by an account's own key proves control only while
// that key can authorise a payment. the first case is shaped like the usdc
// issuer on the public network: master weight 0, thresholds 2/2/2, four
// other signers of weight 1.
func TestSEP53NeedsMasterWeight(t *testing.T) {
	e := start(t)
	owner := e.user(t)
	kp := keypair.MustRandom()
	account := kp.Address()
	tenant := e.tenant(t, owner, []string{account})
	c := e.client(t, owner)
	base := "/api/tenants/" + tenant.String() + "/onchain"
	id := idOf(t, c.call(t, http.MethodGet, base, nil), account)["id"].(string)

	others := []map[string]any{}
	for range 4 {
		others = append(others, map[string]any{"key": keypair.MustRandom().Address(), "weight": 1, "type": "ed25519_public_key"})
	}
	e.l.mu.Lock()
	e.l.data[account] = map[string]string{}
	e.l.signers[account] = append(others, map[string]any{"key": account, "weight": 0, "type": "ed25519_public_key"})
	e.l.thresholds[account] = map[string]int{"low_threshold": 2, "med_threshold": 2, "high_threshold": 2}
	e.l.mu.Unlock()

	sign := func() resp {
		ch := c.call(t, http.MethodPost, base+"/ids/"+id+"/challenge", nil)
		require.Equal(t, http.StatusOK, ch.status, string(ch.raw))
		h := chain.SEP53Hash(ch.body["message"].(string))
		sig, err := kp.Sign(h[:])
		require.NoError(t, err)
		return c.call(t, http.MethodPost, base+"/ids/"+id+"/verify", map[string]any{"method": "sep53", "signature": base64.StdEncoding.EncodeToString(sig)})
	}
	r := sign()
	ver := r.body["verification"].(map[string]any)
	require.Equal(t, "unverified", ver["status"], string(r.raw))
	require.Equal(t, onchain.ErrMasterNoWeight, ver["last_error"])

	// a master key below the medium threshold cannot pay alone either
	e.l.mu.Lock()
	e.l.signers[account] = append(others, map[string]any{"key": account, "weight": 1, "type": "ed25519_public_key"})
	e.l.mu.Unlock()
	r = sign()
	require.Equal(t, onchain.ErrMasterNoWeight, r.body["verification"].(map[string]any)["last_error"], string(r.raw))

	// a challenge replaced while the proof reads the ledger is not spent by
	// the older signature
	e.l.mu.Lock()
	e.l.signers[account] = append(others, map[string]any{"key": account, "weight": 2, "type": "ed25519_public_key"})
	e.l.mu.Unlock()
	e.l.onAccount = func(string) {
		e.sys(t, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `update cp.chain_ids set challenge_nonce = repeat('0', 32) where strkey = $1`, account)
			return err
		})
	}
	r = sign()
	ver = r.body["verification"].(map[string]any)
	require.Equal(t, "unverified", ver["status"], string(r.raw))
	require.Equal(t, onchain.ErrNoChallenge, ver["last_error"])
	e.l.onAccount = nil

	// a master key that reaches the medium threshold proves control
	r = sign()
	ver = r.body["verification"].(map[string]any)
	require.Equal(t, "verified", ver["status"], string(r.raw))
	require.Nil(t, ver["last_error"])
}

// TestContractProofRacesOwnerRemoval: an owner removed while a contract
// proof through it reads the ledger leaves the contract unverified, never
// verified through an id the project no longer declares.
func TestContractProofRacesOwnerRemoval(t *testing.T) {
	e := start(t)
	owner := e.user(t)
	kp := keypair.MustRandom()
	account := kp.Address()
	contract, _, _ := contractID(t, account, 7)
	tenant := e.tenant(t, owner, []string{account, contract})
	c := e.client(t, owner)
	base := "/api/tenants/" + tenant.String() + "/onchain"
	view := c.call(t, http.MethodGet, base, nil)
	accountID := idOf(t, view, account)["id"].(string)
	contractRow := idOf(t, view, contract)["id"].(string)
	e.sys(t, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `update cp.chain_ids set verify_method = 'sep53', verified_at = now() where strkey = $1`, account)
		return err
	})
	e.l.mu.Lock()
	e.l.instances[contract] = instanceWithAdmin(t, contract, account)
	e.l.mu.Unlock()

	// the owner account is removed while the proof reads the instance
	e.l.onRPC = func() {
		e.l.onRPC = nil
		r := c.call(t, http.MethodDelete, base+"/ids/"+accountID, nil)
		require.Equal(t, http.StatusNoContent, r.status, string(r.raw))
	}
	r := c.call(t, http.MethodPost, base+"/ids/"+contractRow+"/verify", map[string]any{"method": "contract_admin"})
	require.Equal(t, http.StatusOK, r.status, string(r.raw))
	ver := r.body["verification"].(map[string]any)
	require.Equal(t, "unverified", ver["status"], string(r.raw))
	require.Equal(t, onchain.ErrAdminNotVerified, ver["last_error"])
	require.Zero(t, e.count(t, `select count(*) from cp.chain_ids where strkey = $1 and verified_at is not null`, contract))
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	e.sys(t, func(tx pgx.Tx) error { return tx.QueryRow(t.Context(), q, args...).Scan(&n) })
	return n
}
