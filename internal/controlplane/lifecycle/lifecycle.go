// Package lifecycle is the tenant lifecycle of the design
// (control-plane-api 5.5 and 5.6): the transition table, the operator
// actions approve, reject, suspend, resume and remove on tenants, approve
// and reject on sites in review, and the removal purge that deletes a
// removed tenant's share of every data store through one Purger per store.
package lifecycle

import (
	"net/http"
	"slices"

	"github.com/justmert/veristar/internal/controlplane/httpx"
	"github.com/justmert/veristar/internal/controlplane/tenants"
)

// tenant statuses of the cp.tenants column check
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusRejected  = "rejected"
	StatusSuspended = "suspended"
	StatusRemoved   = "removed"
)

// Action is an operator action on a tenant or a site in review.
type Action string

// actions of contract 5.5, in the order allowed_actions lists them
const (
	Approve Action = "approve"
	Reject  Action = "reject"
	Suspend Action = "suspend"
	Resume  Action = "resume"
	Remove  Action = "remove"
)

// audit actions of contract 3.3 written by this package
const (
	AuditTenantApprove = "tenant.approve"
	AuditTenantReject  = "tenant.reject"
	AuditTenantSuspend = "tenant.suspend"
	AuditTenantResume  = "tenant.resume"
	AuditTenantRemove  = "tenant.remove"
	AuditSiteApprove   = "site.approve"
	AuditSiteReject    = "site.reject"
	AuditPurgeStep     = "purge.step"
	AuditPurgeComplete = "purge.complete"
)

// ActorPurge is the audit actor of the purge runner.
const ActorPurge = "system:purge"

// CodeInvalidTransition is the 409 code of contract 2.4 for an action that
// is not allowed from the current status.
const CodeInvalidTransition = "invalid_transition"

// transition is one row of the table in the reference docs 4.1.
type transition struct {
	from           []string
	to             string
	reasonRequired bool
	audit          string
}

var tenantTransitions = map[Action]transition{
	Approve: {from: []string{StatusPending}, to: StatusActive, audit: AuditTenantApprove},
	Reject:  {from: []string{StatusPending}, to: StatusRejected, reasonRequired: true, audit: AuditTenantReject},
	Suspend: {from: []string{StatusActive}, to: StatusSuspended, reasonRequired: true, audit: AuditTenantSuspend},
	Resume:  {from: []string{StatusSuspended}, to: StatusActive, audit: AuditTenantResume},
	Remove: {
		from: []string{StatusPending, StatusActive, StatusRejected, StatusSuspended},
		to:   StatusRemoved, reasonRequired: true, audit: AuditTenantRemove,
	},
}

var actionOrder = []Action{Approve, Reject, Suspend, Resume, Remove}

// AllowedActions lists the tenant actions valid from status, for the
// allowed_actions field of contract 5.4. removed is terminal: none.
func AllowedActions(status string) []string {
	out := []string{}
	for _, a := range actionOrder {
		if slices.Contains(tenantTransitions[a].from, status) {
			out = append(out, string(a))
		}
	}
	return out
}

// Next returns the status a tenant action leads to from status, or 409
// invalid_transition. repeating an action on a tenant already in the target
// state is also refused, so a double click never writes two audit rows.
func Next(status string, a Action) (string, error) {
	t, ok := tenantTransitions[a]
	if !ok {
		return "", httpx.NotFound()
	}
	if !slices.Contains(t.from, status) {
		return "", invalidTransition(string(a), status)
	}
	return t.to, nil
}

func invalidTransition(action, status string) *httpx.Error {
	return httpx.Conflict(CodeInvalidTransition, action+" is not allowed while the status is "+status)
}

// reasonMax is the reason length limit of contract 5.2.
const reasonMax = 1000

// ReasonBody is the request body of every action route (contract 5.2).
type ReasonBody struct {
	Reason *string `json:"reason"`
}

// checkReason validates the reason: required where the action needs one,
// and when present always 1 to 1,000 characters after trimming with the
// text rules of contract 3.2. it returns the trimmed value or nil.
func checkReason(b ReasonBody, required bool) (*string, error) {
	var f tenants.Fields
	if b.Reason == nil {
		if required {
			f.Add("reason", httpx.FieldRequired)
			return nil, f.Err()
		}
		return nil, nil
	}
	v := tenants.Text(&f, "reason", *b.Reason, 1, reasonMax)
	if err := f.Err(); err != nil {
		return nil, err
	}
	return &v, nil
}

// decodeReason reads the body of an action route.
func decodeReason(r *http.Request, required bool) (*string, error) {
	var b ReasonBody
	if err := httpx.DecodeJSON(r, &b); err != nil {
		return nil, err
	}
	return checkReason(b, required)
}
