package scfawards

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/justmert/veristar/internal/controlplane/audit"
	"github.com/justmert/veristar/internal/db"
)

// ActionImport is the audit action of an import.
const ActionImport = "scf.import"

// ActorPlatformctl is the audit actor of platformctl scf import and scf
// match.
const ActorPlatformctl = "system:platformctl-scf"

// ImportTarget is the audit target of an import run.
func ImportTarget(id uuid.UUID) string { return "scf_import:" + id.String() }

// maxAuditRejections is the most rejections an import's audit row lists;
// the count is always complete
const maxAuditRejections = 200

// Report is what an import did.
type Report struct {
	ImportID   uuid.UUID
	FileSHA256 string
	FileBytes  int
	HasHeader  bool
	Delimiter  string
	Records    int
	Inserted   int
	Updated    int
	Unchanged  int
	Rejected   []Rejection
	Autofill   AutofillResult
}

// Import parses an export and, in one cp_system transaction, upserts its
// awards by project and round, records the run, writes the audit row and
// runs the matching for every project. a file that is not readable as
// delimited text changes nothing. importing the same file again changes
// no award and writes no proposal; it records the run.
func Import(ctx context.Context, d *db.DB, data []byte, actor string, now time.Time) (Report, error) {
	sum := sha256.Sum256(data)
	rep := Report{FileSHA256: hex.EncodeToString(sum[:]), FileBytes: len(data)}
	p, err := Parse(data, utcDay(now))
	if err != nil {
		return rep, err
	}
	rep.HasHeader, rep.Delimiter, rep.Records, rep.Rejected = p.HasHeader, p.Delimiter, p.Records, p.Rejected
	if rep.Rejected == nil {
		rep.Rejected = []Rejection{}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return rep, err
	}
	rep.ImportID = id
	err = d.WithSystem(ctx, func(tx pgx.Tx) error {
		// one import at a time: the counts compare against the rows as of
		// this lock
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('cp.scf_import', 0))`); err != nil {
			return err
		}
		existing, err := LoadAwards(ctx, tx)
		if err != nil {
			return err
		}
		byKey := make(map[string]AwardRow, len(existing))
		for _, a := range existing {
			byKey[strings.ToLower(a.Project)+"\x00"+a.Round] = a
		}
		type write struct {
			a      Award
			update bool
		}
		var writes []write
		var touch []uuid.UUID
		for _, a := range p.Awards {
			old, ok := byKey[strings.ToLower(a.Project)+"\x00"+a.Round]
			switch {
			case !ok:
				rep.Inserted++
				writes = append(writes, write{a: a})
			case same(old, a):
				rep.Unchanged++
				touch = append(touch, old.ID)
			default:
				rep.Updated++
				writes = append(writes, write{a: a, update: true})
			}
		}
		if _, err := tx.Exec(ctx, `insert into cp.scf_award_imports (id, file_sha256, file_bytes, has_header, delimiter,
			  records, inserted, updated, unchanged, rejected, actor)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			id, sum[:], len(data), p.HasHeader, p.Delimiter, p.Records, rep.Inserted, rep.Updated, rep.Unchanged,
			len(p.Rejected), actor); err != nil {
			return fmt.Errorf("scfawards: record import: %w", err)
		}
		for _, w := range writes {
			a := w.a
			if _, err := tx.Exec(ctx, `
				insert into cp.scf_awards (project, round, category, program, award_date, award_amount, tranche_percent,
				                           tranche_status, website_host, strkeys, import_id)
				values ($1, $2, $3, $4, $5, $6::numeric / 100, $7, $8, $9, $10, $11)
				on conflict (lower(project), round) do update set
				  project = excluded.project, category = excluded.category, program = excluded.program,
				  award_date = excluded.award_date, award_amount = excluded.award_amount,
				  tranche_percent = excluded.tranche_percent, tranche_status = excluded.tranche_status,
				  website_host = excluded.website_host, strkeys = excluded.strkeys,
				  import_id = excluded.import_id, imported_at = now()`,
				a.Project, a.Round, a.Category, a.Program, a.AwardDate, a.AmountCents, a.TranchePercent,
				a.TrancheStatus, a.WebsiteHost, nonNil(a.Strkeys), id); err != nil {
				return fmt.Errorf("scfawards: award on line %d: %w", a.Line, err)
			}
		}
		// an unchanged award records the run that saw it last
		if _, err := tx.Exec(ctx, `update cp.scf_awards set import_id = $1, imported_at = now() where id = any($2::uuid[])`,
			id, touch); err != nil {
			return fmt.Errorf("scfawards: unchanged awards: %w", err)
		}
		rejections := p.Rejected
		if len(rejections) > maxAuditRejections {
			rejections = rejections[:maxAuditRejections]
		}
		if rejections == nil {
			rejections = []Rejection{}
		}
		if err := audit.InSystem(ctx, tx, audit.Entry{
			Actor: actor, Action: ActionImport, Target: ImportTarget(id),
			Detail: map[string]any{
				"import_id": id.String(), "file_sha256": rep.FileSHA256, "file_bytes": len(data),
				"has_header": p.HasHeader, "delimiter": p.Delimiter, "records": p.Records,
				"inserted": rep.Inserted, "updated": rep.Updated, "unchanged": rep.Unchanged,
				"rejected": len(p.Rejected), "rejections": rejections,
			},
		}); err != nil {
			return err
		}
		rep.Autofill, err = Autofill(ctx, tx, actor, nil)
		return err
	})
	return rep, err
}

// same reports whether a stored award holds exactly the parsed values.
func same(old AwardRow, a Award) bool {
	return old.Project == a.Project && old.Category == a.Category && old.Program == a.Program &&
		eqDate(old.AwardDate, a.AwardDate) && old.AmountCents == a.AmountCents &&
		eqInt(old.TranchePercent, a.TranchePercent) && eq(old.TrancheStatus, a.TrancheStatus) &&
		eq(old.WebsiteHost, a.WebsiteHost) && slices.Equal(nonNil(old.Strkeys), nonNil(a.Strkeys))
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func eqDate(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }

func eqInt(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Rematch runs the matching for every project without importing, after
// evidence changed (an on-chain id verified, for example).
func Rematch(ctx context.Context, d *db.DB, actor string) (AutofillResult, error) {
	var res AutofillResult
	err := d.WithSystem(ctx, func(tx pgx.Tx) error {
		var err error
		res, err = Autofill(ctx, tx, actor, nil)
		return err
	})
	return res, err
}
