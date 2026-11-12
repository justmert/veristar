package operator

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/stellar-analytics/internal/controlplane/httpx"
	"github.com/justmert/stellar-analytics/internal/db"
	"github.com/justmert/stellar-analytics/internal/db/systemq"
)

// AuditLog serves GET /api/operator/audit (contract 5.9) under cp_system:
// cp_operator has no right on cp.audit_log, on purpose.
type AuditLog struct {
	db *db.DB
}

// NewAuditLog needs the cp_system pool.
func NewAuditLog(d *db.DB) (*AuditLog, error) {
	if d == nil {
		return nil, errors.New("operator: audit log needs a database")
	}
	return &AuditLog{db: d}, nil
}

// Routes registers the path under the guarded /api/operator router.
func (h *AuditLog) Routes(r chi.Router) {
	r.Method(http.MethodGet, "/audit", httpx.HandlerFunc(h.list))
}

// AuditEntry is one audit row of contract 5.9.
type AuditEntry struct {
	ID         int64           `json:"id"`
	At         httpx.Timestamp `json:"at"`
	Actor      string          `json:"actor"`
	ActorLogin *string         `json:"actor_login"`
	Action     string          `json:"action"`
	Target     string          `json:"target"`
	Detail     json.RawMessage `json:"detail"`
}

// ToAuditEntry converts a row; the tenant detail route (contract 5.4
// recent_audit) uses the same shape.
func ToAuditEntry(r systemq.OperatorAuditPageRow) AuditEntry {
	return AuditEntry{
		ID: r.ID, At: httpx.Timestamp(r.At), Actor: r.Actor, ActorLogin: r.ActorLogin,
		Action: r.Action, Target: r.Target, Detail: json.RawMessage(r.Detail),
	}
}

// filter limits of contract 5.9
const (
	auditRoute       = "operator-audit"
	auditMaxFilter   = 200
	auditMaxInterval = 400 * 24 * time.Hour
)

// auditCursor is the keyset position (at, id). the full timestamp
// precision is kept, so rows in the same millisecond are not skipped.
type auditCursor struct {
	R string    `json:"r"`
	T time.Time `json:"t"`
	I int64     `json:"i"`
}

func encodeAuditCursor(at time.Time, id int64) *string {
	b, err := json.Marshal(auditCursor{R: auditRoute, T: at.UTC(), I: id})
	if err != nil {
		// a time and an int always encode
		panic(err)
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

func decodeAuditCursor(v string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return time.Time{}, 0, httpx.BadCursor()
	}
	var c auditCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.R != auditRoute || c.T.IsZero() || c.I <= 0 {
		return time.Time{}, 0, httpx.BadCursor()
	}
	return c.T, c.I, nil
}

// likeEscaper makes a literal like pattern; action names contain "_".
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// one value per parameter, never an empty one
func single(q url.Values, key string) (*string, error) {
	v, ok := q[key]
	if !ok {
		return nil, nil
	}
	if len(v) != 1 || v[0] == "" || utf8.RuneCountInString(v[0]) > auditMaxFilter || !utf8.ValidString(v[0]) {
		return nil, httpx.BadQuery(key + " must be given once, 1 to 200 characters")
	}
	return &v[0], nil
}

func parseAuditQuery(r *http.Request) (systemq.OperatorAuditPageParams, int, error) {
	q := r.URL.Query()
	var p systemq.OperatorAuditPageParams
	for key := range q {
		switch key {
		case "tenant_id", "target", "action", "actor", "from", "to", "limit", "cursor":
		default:
			return p, 0, httpx.BadQuery("unknown query parameter " + key)
		}
	}
	limit := httpx.DefaultLimit
	if v, ok := q["limit"]; ok {
		n, err := strconv.Atoi(v[0])
		if len(v) != 1 || err != nil || n < 1 || n > httpx.MaxLimit {
			return p, 0, httpx.BadQuery("limit must be an integer between 1 and 200")
		}
		limit = n
	}
	if v, ok := q["cursor"]; ok {
		if len(v) != 1 {
			return p, 0, httpx.BadCursor()
		}
		at, id, err := decodeAuditCursor(v[0])
		if err != nil {
			return p, 0, err
		}
		p.AfterAt, p.AfterID = &at, &id
	}

	tenant, err := single(q, "tenant_id")
	if err != nil {
		return p, 0, err
	}
	if tenant != nil {
		id, err := uuid.Parse(*tenant)
		if err != nil || id.String() != *tenant {
			return p, 0, httpx.BadQuery("tenant_id must be a lowercase uuid")
		}
		p.TenantID = tenant
	}
	if p.Target, err = single(q, "target"); err != nil {
		return p, 0, err
	}
	if p.Actor, err = single(q, "actor"); err != nil {
		return p, 0, err
	}
	action, err := single(q, "action")
	if err != nil {
		return p, 0, err
	}
	if action != nil {
		if strings.HasSuffix(*action, ".") {
			// a prefix such as "tenant." matches every tenant action
			like := likeEscaper.Replace(*action) + "%"
			p.ActionLike = &like
		} else {
			p.Action = action
		}
	}
	for _, k := range []string{"from", "to"} {
		v, err := single(q, k)
		if err != nil {
			return p, 0, err
		}
		if v == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, *v)
		if err != nil {
			return p, 0, httpx.BadQuery(k + " must be an rfc 3339 time")
		}
		if k == "from" {
			p.FromAt = &t
		} else {
			p.ToAt = &t
		}
	}
	if p.FromAt != nil && p.ToAt != nil {
		if !p.FromAt.Before(*p.ToAt) {
			return p, 0, httpx.BadQuery("from must be before to")
		}
		if p.ToAt.Sub(*p.FromAt) > auditMaxInterval {
			return p, 0, httpx.BadQuery("from and to must be at most 400 days apart")
		}
	}
	// one row more than the page tells whether another page exists
	p.Lim = int32(limit + 1)
	return p, limit, nil
}

func (h *AuditLog) list(w http.ResponseWriter, r *http.Request) error {
	p, limit, err := parseAuditQuery(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var rows []systemq.OperatorAuditPageRow
	if err := h.db.WithSystem(ctx, func(tx pgx.Tx) error {
		var qerr error
		rows, qerr = systemq.New(tx).OperatorAuditPage(ctx, p)
		return qerr
	}); err != nil {
		return fmt.Errorf("operator: audit page: %w", err)
	}
	out := httpx.List[AuditEntry]{Items: make([]AuditEntry, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			last := rows[limit-1]
			out.NextCursor = encodeAuditCursor(last.At, last.ID)
			break
		}
		out.Items = append(out.Items, ToAuditEntry(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
