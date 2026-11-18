package scfawards

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/db"
)

// Hooks run the matching for one project when it enrolls or adds a site,
// so a funded project gets its cohort proposal at once instead of at the
// next import.
type Hooks struct {
	db      *db.DB
	log     *slog.Logger
	running sync.WaitGroup
}

// NewHooks needs the database with the cp_system pool and a log.
func NewHooks(d *db.DB, log *slog.Logger) (*Hooks, error) {
	if d == nil || log == nil {
		return nil, errors.New("scfawards: the hooks need a database and a log")
	}
	return &Hooks{db: d, log: log}, nil
}

// AfterEnroll runs the matching for the new project inside its enrollment
// transaction (cp_system), so the enrollment and its proposal commit
// together or not at all.
func (h *Hooks) AfterEnroll(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	_, err := Autofill(ctx, tx, ActorMatch, []uuid.UUID{tenantID})
	return err
}

// siteTimeout bounds the matching after a site is created
const siteTimeout = time.Minute

// AfterSiteCreate runs the matching for the project in the background
// after its new site committed, as the site routes require of their hooks.
// a failure is logged; the next import or platformctl scf match runs the
// same matching again.
func (h *Hooks) AfterSiteCreate(ctx context.Context, tenantID, siteID uuid.UUID, _ string, _ string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), siteTimeout)
	h.running.Add(1)
	go func() {
		defer h.running.Done()
		defer cancel()
		err := h.db.WithSystem(ctx, func(tx pgx.Tx) error {
			_, err := Autofill(ctx, tx, ActorMatch, []uuid.UUID{tenantID})
			return err
		})
		if err != nil {
			h.log.Error("scf award matching after site create failed; the next import or platformctl scf match runs it again",
				"tenant_id", tenantID.String(), "site_id", siteID.String(), "error", err.Error())
		}
	}()
}

// Wait blocks until every matching started by AfterSiteCreate returned;
// the server calls it on shutdown.
func (h *Hooks) Wait() { h.running.Wait() }
