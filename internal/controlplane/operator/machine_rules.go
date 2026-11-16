package operator

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/httpx"
)

// the threshold rules of the machine api (contract 9.11 and 19.4): the
// saved rules, and the projects that meet one with the evidence. a key
// scoped to one tenant evaluates that tenant only.

func (m *Machine) rules(w http.ResponseWriter, r *http.Request) error {
	if err := checkParams(r.URL.Query()); err != nil {
		return err
	}
	ctx := r.Context()
	var out RuleList
	err := m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		var err error
		out.Items, err = ListRules(ctx, tx)
		return err
	})
	if err != nil {
		return fmt.Errorf("operator: machine rules: %w", err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (m *Machine) ruleMatches(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := checkParams(q, "tenant", "as_of"); err != nil {
		return err
	}
	id, err := httpx.ParseID(chi.URLParam(r, "rule_id"))
	if err != nil {
		return err
	}
	asQ := q
	if _, ok := q["tenant"]; ok {
		asQ = withoutKey(q, "tenant")
	}
	asOf, err := parseAsOf(asQ, m.now())
	if err != nil {
		return err
	}
	ctx := r.Context()
	key := keyFrom(ctx)
	var out RuleMatches
	err = m.db.WithOperator(ctx, func(tx pgx.Tx) error {
		if err := snapshot(ctx, tx); err != nil {
			return err
		}
		sc, err := resolveScope(ctx, tx, key, q)
		if err != nil {
			return err
		}
		rule, err := getRule(ctx, tx, id, false)
		if err != nil {
			return err
		}
		out, err = EvaluateRule(ctx, tx, rule, asOf, sc.One)
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out.meeting())
	return nil
}

func withoutKey(q map[string][]string, key string) map[string][]string {
	out := make(map[string][]string, len(q))
	for k, v := range q {
		if k != key {
			out[k] = v
		}
	}
	return out
}
