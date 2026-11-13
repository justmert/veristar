// Package erasure is the tenant-triggered erasure workflow (the design,
// control-plane-api section 10). a project owner asks to erase one visitor's
// visit of today, one umami session or every web record of a site. the
// route checks the request, records it in cp.erasure_requests and answers
// 202; a run in this process then waits until the replayer has taken every
// event of the site that was queued before the request, deletes the umami
// sessions or resets the websites, deletes matching WEB_DLQ messages and
// visit records, and records the counts. the jobs task erasure-followup
// reads the affected days of agg.web_daily again.
//
// a visitor_today request carries the data subject's address and user
// agent. they stay in this process's memory for the request only: they go
// to the gateways in one nats request, which answers with the visitor token,
// and no log line, error, audit row or table ever holds them.
package erasure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/db"
	"github.com/justmert/veristar/internal/db/systemq"
	"github.com/justmert/veristar/internal/natsx"
	"github.com/justmert/veristar/internal/replayer"
	"github.com/justmert/veristar/internal/umami"
)

// kinds of cp.erasure_requests
const (
	KindVisitorToday = "visitor_today"
	KindSession      = "session"
	KindSiteReset    = "site_reset"
)

// outcomes of a finished request
const (
	OutcomeErased       = "erased"
	OutcomeNothingFound = "nothing_found"
)

// statuses of cp.erasure_requests
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// HeartbeatEvery is how often a run renews heartbeat_at. StaleAfter is how
// long the jobs task waits after the last renewal before it fails the run.
const (
	HeartbeatEvery = 10 * time.Second
	StaleAfter     = 2 * time.Minute
)

// runTimeout bounds one run after the queue wait: umami calls, dlq and visit
// state cleanup.
const runTimeout = 30 * time.Minute

// Umami is the part of the umami admin client a run uses.
type Umami interface {
	GetSession(ctx context.Context, websiteID, sessionID string) (umami.SessionDetail, bool, error)
	DeleteSession(ctx context.Context, websiteID, sessionID string) error
	ResetWebsite(ctx context.Context, id string) error
	SessionStats(ctx context.Context, websiteID string, r umami.Range) (umami.SessionTotals, error)
}

// Config wires a Service.
type Config struct {
	DB    *db.DB
	NC    *nats.Conn
	Umami Umami
	// QueueWait bounds the wait for the site's queued events
	// (ERASURE_QUEUE_WAIT).
	QueueWait time.Duration
	Now       func() time.Time
	Log       *slog.Logger
}

// Service runs erasure requests.
type Service struct {
	cfg Config
	js  jetstream.JetStream

	running sync.WaitGroup
}

// New returns a Service. the bucket and streams are bound per use: visit_state
// is memory-backed and the replayer recreates it after a nats restart, so it
// can be missing for a moment, and the control plane must start anyway.
func New(cfg Config) (*Service, error) {
	if cfg.DB == nil || cfg.NC == nil || cfg.Umami == nil || cfg.Now == nil || cfg.Log == nil || cfg.QueueWait <= 0 {
		return nil, errors.New("erasure: Config needs DB, NC, Umami, Now, Log and a positive QueueWait")
	}
	js, err := jetstream.New(cfg.NC)
	if err != nil {
		return nil, fmt.Errorf("erasure: jetstream: %w", err)
	}
	return &Service{cfg: cfg, js: js}, nil
}

// stores are the nats objects a run uses.
type stores struct {
	visits jetstream.KeyValue
	events jetstream.Stream
	dlq    jetstream.Stream
}

func (s *Service) bind(ctx context.Context) (stores, error) {
	var st stores
	var err error
	if st.visits, err = s.js.KeyValue(ctx, natsx.BucketVisitState); err != nil {
		return st, fmt.Errorf("erasure: bind %s: %w", natsx.BucketVisitState, err)
	}
	if st.events, err = s.js.Stream(ctx, natsx.StreamWebEvents); err != nil {
		return st, fmt.Errorf("erasure: bind %s: %w", natsx.StreamWebEvents, err)
	}
	if st.dlq, err = s.js.Stream(ctx, natsx.StreamWebDLQ); err != nil {
		return st, fmt.Errorf("erasure: bind %s: %w", natsx.StreamWebDLQ, err)
	}
	return st, nil
}

// Wait blocks until every started run has finished. the server calls it on
// shutdown, after the listener stopped taking requests.
func (s *Service) Wait() { s.running.Wait() }

// job is what a run needs. vt is set for visitor_today only and exists
// nowhere but here.
type job struct {
	req        systemq.CpErasureRequest
	quarantine string
	siteFrom   time.Time
	vt         string
	snap       uint64
}

// counts is what a run deleted so far, reported also when it fails.
type counts struct {
	sessions, events, dlq int64
	from, to              *time.Time
}

func (c *counts) affect(first, last time.Time) {
	f, l := utcDay(first), utcDay(last)
	if c.from == nil || f.Before(*c.from) {
		c.from = &f
	}
	if c.to == nil || l.After(*c.to) {
		c.to = &l
	}
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// start runs j in the background with its own context: the request that
// created it has already been answered.
func (s *Service) start(j job) {
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		s.run(j)
	}()
}

func (s *Service) run(j job) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.QueueWait+runTimeout)
	defer cancel()
	hbDone := make(chan struct{})
	go s.heartbeat(ctx, j.req.ID, hbDone)
	defer close(hbDone)

	log := s.cfg.Log.With("erasure_id", j.req.ID.String(), "kind", j.req.Kind, "site_id", j.req.SiteID.String())
	var c counts
	outcome, message, err := s.erase(ctx, j, &c)
	if err != nil {
		// errors of a run never carry an address, user agent or token: the
		// gateway, umami and nats errors quote none of them
		log.Error("erasure failed", "error", err.Error())
		s.finish(j, &c, "", "", err.Error())
		return
	}
	log.Info("erasure done", "outcome", outcome, "sessions", c.sessions, "events", c.events, "dlq", c.dlq)
	s.finish(j, &c, outcome, message, "")
}

func (s *Service) heartbeat(ctx context.Context, id uuid.UUID, done <-chan struct{}) {
	t := time.NewTicker(HeartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.cfg.DB.WithSystem(ctx, func(tx pgx.Tx) error {
				_, err := systemq.New(tx).ErasureHeartbeat(ctx, id)
				return err
			}); err != nil && ctx.Err() == nil {
				s.cfg.Log.Warn("erasure heartbeat not written", "erasure_id", id.String(), "error", err.Error())
			}
		}
	}
}

// finish records the result. it gets its own context, so a run cut off by
// its timeout still records why.
func (s *Service) finish(j job, c *counts, outcome, message, failure string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := s.cfg.DB.WithSystem(ctx, func(tx pgx.Tx) error {
		q := systemq.New(tx)
		action := audit.ErasureComplete
		detail := map[string]any{
			"tenant_id": j.req.TenantID, "site_id": j.req.SiteID, "kind": j.req.Kind,
			"sessions_deleted": c.sessions, "events_deleted": c.events, "dlq_messages_deleted": c.dlq,
		}
		if failure != "" {
			action = audit.ErasureFail
			detail["error"] = failure
			if _, err := q.FailErasureRequest(ctx, systemq.FailErasureRequestParams{
				ID: j.req.ID, Error: &failure, SessionsDeleted: c.sessions, EventsDeleted: c.events,
				DlqMessagesDeleted: c.dlq, AffectedFrom: c.from, AffectedTo: c.to,
			}); err != nil {
				return err
			}
		} else {
			detail["outcome"] = outcome
			if _, err := q.CompleteErasureRequest(ctx, systemq.CompleteErasureRequestParams{
				ID: j.req.ID, Outcome: &outcome, Message: &message, SessionsDeleted: c.sessions,
				EventsDeleted: c.events, DlqMessagesDeleted: c.dlq, AffectedFrom: c.from, AffectedTo: c.to,
			}); err != nil {
				return err
			}
		}
		return audit.InSystem(ctx, tx, audit.Entry{
			Actor: "system:erasure", Action: action, Target: audit.ErasureTarget(j.req.ID), Detail: detail,
		})
	})
	if err != nil {
		// the row stays running; erasure-followup fails it once the
		// heartbeat is stale, so the tenant sees a result either way
		s.cfg.Log.Error("erasure result not recorded", "erasure_id", j.req.ID.String(), "error", err.Error())
	}
}

// erase does the work of one request after the queue passed it.
func (s *Service) erase(ctx context.Context, j job, c *counts) (outcome, message string, err error) {
	site := j.req.SiteID.String()
	st, err := s.bind(ctx)
	if err != nil {
		return "", "", err
	}
	qctx, qcancel := context.WithTimeout(ctx, s.cfg.QueueWait+time.Minute)
	err = waitQueue(qctx, st.events, site, j.snap, s.cfg.QueueWait)
	qcancel()
	if errors.Is(err, errQueueBehind) {
		return "", "", fmt.Errorf("events of this site queued before the request were not delivered within %s "+
			"(the replayer or umami is behind); nothing was deleted, submit the request again later", s.cfg.QueueWait)
	}
	if err != nil {
		return "", "", err
	}
	switch j.req.Kind {
	case KindVisitorToday:
		return s.visitorToday(ctx, st, j, c)
	case KindSession:
		return s.session(ctx, st, j, c)
	case KindSiteReset:
		return s.siteReset(ctx, st, j, c)
	default:
		return "", "", fmt.Errorf("erasure: unknown kind %q", j.req.Kind)
	}
}

// deleteSession deletes one umami session after reading its event counts
// and days. found is false when umami has no such session.
func (s *Service) deleteSession(ctx context.Context, website, session string, c *counts) (found bool, err error) {
	d, ok, err := s.cfg.Umami.GetSession(ctx, website, session)
	if err != nil {
		return false, err
	}
	// umami answers 404 for a session without events too, so the delete is
	// tried either way; it answers 404 only when the session row is absent
	err = s.cfg.Umami.DeleteSession(ctx, website, session)
	if errors.Is(err, umami.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c.sessions++
	if ok {
		c.events += d.Views.Int64() + d.Events.Int64()
		c.affect(d.FirstAt, d.LastAt)
	}
	return true, nil
}

func (s *Service) websites(j job) map[string]bool {
	return map[string]bool{j.req.SiteID.String(): true, j.quarantine: true}
}

func (s *Service) visitorToday(ctx context.Context, st stores, j job, c *counts) (string, string, error) {
	site := j.req.SiteID.String()
	day := utcDay(*j.req.Day)
	rec, err := visitRecord(ctx, st.visits, site, j.vt)
	if err != nil {
		return "", "", err
	}
	// sessions an earlier run of today recorded before it failed
	pending, err := pendingErasure(ctx, st.visits, site, j.vt)
	if err != nil {
		return "", "", err
	}
	sessions := mergeVisits(rec, pending)
	allowed := s.websites(j)
	for website := range sessions {
		if !allowed[website] {
			return "", "", fmt.Errorf("erasure: visit record names website %s, which is not a website of the site", website)
		}
	}
	if len(sessions) > 0 {
		// the sessions are written down first, so a run that fails after
		// the record went still leaves them for the next request
		if err := savePending(ctx, st.visits, site, j.vt, &replayer.VisitRecord{Websites: sessions}); err != nil {
			return "", "", err
		}
	}
	if rec != nil {
		// the record goes before the sessions, so an event of this visitor
		// that arrives while the sessions are deleted is sent without the
		// old cache token and starts a new session
		if err := purgeVisit(ctx, st.visits, site, j.vt); err != nil {
			return "", "", err
		}
	}
	for website, e := range sessions {
		// a session an earlier run already deleted answers not found
		if _, err := s.deleteSession(ctx, website, e.SessionID, c); err != nil {
			return "", "", err
		}
	}
	n, err := deleteDLQ(ctx, st.dlq, site, containsToken(j.vt))
	c.dlq += n
	if err != nil {
		return "", "", err
	}
	if len(sessions) > 0 {
		if err := clearPending(ctx, st.visits, site, j.vt); err != nil {
			return "", "", err
		}
	}
	date := day.Format(time.DateOnly)
	if len(sessions) == 0 && c.dlq == 0 {
		return OutcomeNothingFound, s.notLocated(ctx, st, day), nil
	}
	msg := fmt.Sprintf("Deleted %d session(s) with %d event(s) of this visitor on %s (UTC) "+
		"and %d undeliverable queued message(s).", c.sessions, c.events, date, c.dlq)
	if pending != nil {
		msg += " This request finished the erasure an earlier request of today started."
	}
	return OutcomeErased, msg, nil
}

// mergeVisits joins the sessions of the visit record and of an earlier
// run's progress record, by website.
func mergeVisits(recs ...*replayer.VisitRecord) map[string]replayer.VisitEntry {
	out := map[string]replayer.VisitEntry{}
	for _, r := range recs {
		if r == nil {
			continue
		}
		for website, e := range r.Websites {
			if e.SessionID != "" {
				out[website] = e
			}
		}
	}
	return out
}

// notLocated is the answer when no link from the visitor token to a session
// exists. it says what is known: the link lives in a memory bucket for
// the day, so a missing link does not prove there was no visit.
func (s *Service) notLocated(ctx context.Context, st stores, day time.Time) string {
	date := day.Format(time.DateOnly)
	msg := fmt.Sprintf("A visit by this address and user agent on %s (UTC) could not be located. ", date)
	since, err := bucketSince(ctx, st.visits)
	switch {
	case err != nil:
		s.cfg.Log.Warn("erasure: visit link store age unknown", "error", err.Error())
		msg += "Whether the store that links visitors to their sessions was emptied today could not be checked. "
	case since.After(day):
		msg += fmt.Sprintf("The store that links visitors to their sessions is kept in memory only and was emptied at %s UTC "+
			"today (a restart of the messaging cluster), so a visit before that time cannot be located, although it may "+
			"still be stored. ", since.UTC().Format("15:04"))
	default:
		msg += "The store that links visitors to their sessions has kept every link since the start of the day. "
	}
	msg += "A restart that also replaced the daily secret changes this visitor's token, and a visit before it cannot be " +
		"linked either. To remove every web record that may remain, a site reset erases all data of the site. " +
		"Visits on earlier days cannot be linked to a person: the daily secret that produced their visitor token was " +
		"destroyed when that day ended, so this service cannot identify them (GDPR Article 11)."
	return msg
}

func (s *Service) session(ctx context.Context, st stores, j job, c *counts) (string, string, error) {
	site := j.req.SiteID.String()
	sid := j.req.SessionID.UUID.String()
	found := false
	for _, website := range []string{site, j.quarantine} {
		ok, err := s.deleteSession(ctx, website, sid, c)
		if err != nil {
			return "", "", err
		}
		found = found || ok
	}
	// the visit record links the session to its visitor token while the
	// record exists (24 hours after the visitor's last event); through it
	// the visitor's undeliverable messages are found and the record goes
	vts, err := siteVisits(ctx, st.visits, site)
	if err != nil {
		return "", "", err
	}
	for _, vt := range vts {
		rec, err := visitRecord(ctx, st.visits, site, vt)
		if err != nil {
			return "", "", err
		}
		if rec == nil || !holdsSession(rec.Websites, sid) {
			continue
		}
		if err := purgeVisit(ctx, st.visits, site, vt); err != nil {
			return "", "", err
		}
		n, err := deleteDLQ(ctx, st.dlq, site, containsToken(vt))
		c.dlq += n
		if err != nil {
			return "", "", err
		}
	}
	if !found {
		return OutcomeNothingFound, "No session with this id exists for this site.", nil
	}
	return OutcomeErased, fmt.Sprintf("Deleted the session with %d event(s) and %d undeliverable queued message(s).",
		c.events, c.dlq), nil
}

func holdsSession(m map[string]replayer.VisitEntry, sid string) bool {
	for _, e := range m {
		if e.SessionID == sid {
			return true
		}
	}
	return false
}

func (s *Service) siteReset(ctx context.Context, st stores, j job, c *counts) (string, string, error) {
	site := j.req.SiteID.String()
	// the counts are read before the reset; umami's session stats count
	// sessions with at least one event, page views and custom events
	all := umami.Range{Start: j.siteFrom.Add(-24 * time.Hour), End: s.cfg.Now().Add(24 * time.Hour)}
	for _, website := range []string{site, j.quarantine} {
		st, err := s.cfg.Umami.SessionStats(ctx, website, all)
		if err != nil {
			return "", "", err
		}
		if err := s.cfg.Umami.ResetWebsite(ctx, website); err != nil {
			return "", "", err
		}
		c.sessions += st.Visitors
		c.events += st.Pageviews + st.Events
	}
	// every aggregate of the site goes, whatever its day
	c.affect(j.siteFrom, s.cfg.Now())
	n, err := deleteDLQ(ctx, st.dlq, site, func([]byte) bool { return true })
	c.dlq += n
	if err != nil {
		return "", "", err
	}
	vts, err := siteVisits(ctx, st.visits, site)
	if err != nil {
		return "", "", err
	}
	for _, vt := range vts {
		if err := purgeVisit(ctx, st.visits, site, vt); err != nil {
			return "", "", err
		}
	}
	// progress records of failed visitor runs name sessions the reset
	// just deleted
	pend, err := sitePending(ctx, st.visits, site)
	if err != nil {
		return "", "", err
	}
	for _, vt := range pend {
		if err := clearPending(ctx, st.visits, site, vt); err != nil {
			return "", "", err
		}
	}
	return OutcomeErased, fmt.Sprintf("Deleted every web record of this site: %d session(s) with %d event(s) "+
		"and %d undeliverable queued message(s).", c.sessions, c.events, c.dlq), nil
}
