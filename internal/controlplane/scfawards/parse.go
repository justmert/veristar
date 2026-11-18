package scfawards

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// field is one column the import maps. every other column of the file is
// read past and never kept.
type field int

const (
	fProject field = iota
	fRound
	fCategory
	fAwardType
	fAwardDate
	fAwardAmount
	fTranchePercent
	fTrancheStatus
	fWebsite
	fieldCount
)

var fieldNames = [fieldCount]string{
	"project", "round", "category", "award_type", "award_date", "award_amount",
	"tranche_percent", "tranche_status", "website",
}

// String is the field's name in a rejection.
func (f field) String() string { return fieldNames[f] }

// positions are the columns of a file without a header row: the layout of
// the scf awards file (column 0 repeats the project name, column 25
// names a person and is never read).
var positions = [fieldCount]int{
	fProject: 3, fRound: 1, fCategory: 4, fAwardType: 11, fAwardDate: 10, fAwardAmount: 7,
	fTranchePercent: 6, fTrancheStatus: 32, fWebsite: 16,
}

// minColumns is the fewest columns a row of a file without a header needs:
// through the award type. later columns that are missing are empty.
const minColumns = 12

// headerNames are the header cells each field answers to, folded to lower
// case with every run of other characters as one space.
var headerNames = [fieldCount][]string{
	fProject:        {"project", "project name"},
	fRound:          {"round", "scf round", "award round"},
	fCategory:       {"category", "award category"},
	fAwardType:      {"award type", "type", "program"},
	fAwardDate:      {"award date", "date awarded", "awarded date", "awarded on"},
	fAwardAmount:    {"award amount", "amount", "awarded amount", "award amount usd", "total award"},
	fTranchePercent: {"tranche", "tranche %", "tranche percent", "tranches paid", "percent paid", "tranche completion"},
	fTrancheStatus:  {"status", "tranche status", "stage"},
	fWebsite:        {"website", "project website", "website url", "url"},
}

// requiredHeaders must all be present in a header row.
var requiredHeaders = []field{fProject, fRound, fCategory, fAwardType, fAwardAmount}

var nonWord = regexp.MustCompile(`[^a-z0-9%&]+`)

func headerKey(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// Award is one accepted row: organisation data only.
type Award struct {
	// Record is the row's number among the data rows, from 1; Line is the
	// file line it starts on
	Record, Line int
	Project      string
	Round        string
	Category     string
	Program      string
	AwardDate    *time.Time
	// AmountCents is the award in us cents
	AmountCents    int64
	TranchePercent *int
	TrancheStatus  *string
	WebsiteHost    *string
	Strkeys        []string
}

// Rejection is a row the import refused, with the first reason found. it
// never carries the row's values.
type Rejection struct {
	Record int    `json:"record"`
	Line   int    `json:"line"`
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// rejection reasons
const (
	ReasonMissing          = "missing"
	ReasonInvalid          = "invalid"
	ReasonUnknown          = "unknown_value"
	ReasonOutOfRange       = "out_of_range"
	ReasonTooFewColumns    = "too_few_columns"
	ReasonDuplicate        = "duplicate_project_round"
	ReasonTooManyStellarID = "too_many_stellar_ids"
)

// Parsed is the result of Parse.
type Parsed struct {
	HasHeader bool
	// Delimiter is "tab" or "comma"
	Delimiter string
	// Records counts the data rows, accepted and rejected
	Records  int
	Awards   []Award
	Rejected []Rejection
}

// MaxFileBytes is the largest export the import reads.
const MaxFileBytes = 64 << 20

// Parse reads an export: tab or comma separated (the first line decides),
// with or without a header row (a first row naming at least the project and
// the round is a header), quoted fields over several lines allowed. it
// fails only on a file it cannot read as delimited text at all; a row with
// a bad value is rejected with its reason and the rest go on. today is the
// last award date accepted.
func Parse(data []byte, today time.Time) (Parsed, error) {
	var p Parsed
	if len(data) > MaxFileBytes {
		return p, fmt.Errorf("scfawards: the file is larger than %d bytes", MaxFileBytes)
	}
	if !utf8.Valid(data) {
		return p, errors.New("scfawards: the file is not utf-8 text")
	}
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	first, _, _ := bytes.Cut(data, []byte("\n"))
	r := csv.NewReader(bytes.NewReader(data))
	p.Delimiter = "comma"
	if bytes.ContainsRune(first, '\t') {
		r.Comma = '\t'
		p.Delimiter = "tab"
	}
	// awards files write quotes inside unquoted fields (a description that
	// quotes a word); they are text, as spreadsheet tools read them
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false

	cols := positions
	seen := map[string]int{}
	firstRow := true
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return p, fmt.Errorf("scfawards: the file is not readable as %s separated text: %w", p.Delimiter, err)
		}
		line, _ := r.FieldPos(0)
		if firstRow {
			firstRow = false
			if h, ok, err := header(rec); err != nil {
				return p, err
			} else if ok {
				p.HasHeader, cols = true, h
				continue
			}
		}
		if blank(rec) {
			continue
		}
		p.Records++
		a, rej := parseRow(rec, cols, p.HasHeader, today)
		a.Record, a.Line = p.Records, line
		if rej != nil {
			rej.Record, rej.Line = p.Records, line
			p.Rejected = append(p.Rejected, *rej)
			continue
		}
		key := strings.ToLower(a.Project) + "\x00" + a.Round
		if _, dup := seen[key]; dup {
			p.Rejected = append(p.Rejected, Rejection{Record: p.Records, Line: line, Field: "project", Reason: ReasonDuplicate})
			continue
		}
		seen[key] = p.Records
		p.Awards = append(p.Awards, a)
	}
	return p, nil
}

func blank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// header maps a header row's columns, or reports that rec is a data row.
// a header that names the project and the round but misses another
// required column is an error: the file cannot be read correctly.
func header(rec []string) ([fieldCount]int, bool, error) {
	var cols [fieldCount]int
	for i := range cols {
		cols[i] = -1
	}
	for i, c := range rec {
		k := headerKey(c)
		for f, names := range headerNames {
			for _, n := range names {
				if k == n && cols[f] < 0 {
					cols[f] = i
				}
			}
		}
	}
	if cols[fProject] < 0 || cols[fRound] < 0 {
		return cols, false, nil
	}
	var missing []string
	for _, f := range requiredHeaders {
		if cols[f] < 0 {
			missing = append(missing, f.String())
		}
	}
	if len(missing) > 0 {
		return cols, false, fmt.Errorf("scfawards: the header row has no column for %s", strings.Join(missing, ", "))
	}
	return cols, true, nil
}

// maxAmountCents is the largest award the column holds (numeric(14, 2))
const maxAmountCents = 999_999_999_999_99

var (
	amountPlain     = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,2})?$`)
	amountThousands = regexp.MustCompile(`^[0-9]{1,3}(,[0-9]{3})+(\.[0-9]{1,2})?$`)
	percentRe       = regexp.MustCompile(`^([0-9]{1,3})(\.0+)?\s*%?$`)
)

// earliestAward is the first award date accepted, as in the cohort model
var earliestAward = time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)

func parseRow(rec []string, cols [fieldCount]int, hasHeader bool, today time.Time) (Award, *Rejection) {
	var a Award
	if !hasHeader && len(rec) < minColumns {
		return a, &Rejection{Field: "row", Reason: ReasonTooFewColumns}
	}
	get := func(f field) string {
		i := cols[f]
		if i < 0 || i >= len(rec) {
			return ""
		}
		return rec[i]
	}
	reject := func(f field, reason string) (Award, *Rejection) {
		return Award{}, &Rejection{Field: f.String(), Reason: reason}
	}

	a.Project = collapse(get(fProject))
	switch n := utf8.RuneCountInString(a.Project); {
	case n == 0:
		return reject(fProject, ReasonMissing)
	case n > 200:
		return reject(fProject, ReasonInvalid)
	}
	raw := get(fRound)
	if strings.TrimSpace(raw) == "" {
		return reject(fRound, ReasonMissing)
	}
	var ok bool
	if a.Round, ok = NormalizeRound(raw); !ok {
		return reject(fRound, ReasonInvalid)
	}
	raw = get(fCategory)
	if strings.TrimSpace(raw) == "" {
		return reject(fCategory, ReasonMissing)
	}
	if a.Category, ok = Categories[fold(raw)]; !ok {
		return reject(fCategory, ReasonUnknown)
	}
	raw = get(fAwardType)
	if strings.TrimSpace(raw) == "" {
		return reject(fAwardType, ReasonMissing)
	}
	if a.Program, ok = programs[fold(raw)]; !ok {
		return reject(fAwardType, ReasonUnknown)
	}
	if raw = strings.TrimSpace(get(fAwardDate)); raw != "" {
		d, err := parseDate(raw)
		if err != nil {
			return reject(fAwardDate, ReasonInvalid)
		}
		if d.Before(earliestAward) || d.After(today) {
			return reject(fAwardDate, ReasonOutOfRange)
		}
		a.AwardDate = &d
	}
	raw = get(fAwardAmount)
	if strings.TrimSpace(raw) == "" {
		return reject(fAwardAmount, ReasonMissing)
	}
	cents, err := parseAmount(raw)
	if err != nil {
		return reject(fAwardAmount, ReasonInvalid)
	}
	a.AmountCents = cents
	if raw = strings.TrimSpace(get(fTranchePercent)); raw != "" {
		m := percentRe.FindStringSubmatch(raw)
		if m == nil {
			return reject(fTranchePercent, ReasonInvalid)
		}
		n, _ := strconv.Atoi(m[1])
		if n > 100 {
			return reject(fTranchePercent, ReasonOutOfRange)
		}
		a.TranchePercent = &n
	}
	if raw = collapse(get(fTrancheStatus)); raw != "" {
		if utf8.RuneCountInString(raw) > 80 {
			return reject(fTrancheStatus, ReasonInvalid)
		}
		status := raw
		a.TrancheStatus = &status
	}
	if raw = strings.TrimSpace(get(fWebsite)); raw != "" {
		h, ok := NormalizeHost(raw)
		if !ok {
			return reject(fWebsite, ReasonInvalid)
		}
		a.WebsiteHost = &h
	}
	// the stellar ids anywhere in the row; only the ids are kept, never
	// the text around them
	a.Strkeys = ExtractStrkeys(rec...)
	if len(a.Strkeys) > maxStrkeys {
		return Award{}, &Rejection{Field: "stellar_ids", Reason: ReasonTooManyStellarID}
	}
	return a, nil
}

// parseDate reads m/d/yyyy, the files' form, or yyyy-mm-dd.
func parseDate(s string) (time.Time, error) {
	if d, err := time.Parse(time.DateOnly, s); err == nil {
		return d, nil
	}
	return time.Parse("1/2/2006", s)
}

// parseAmount reads a us dollar amount: an optional "$" or "USD", digits
// with optional thousands commas and up to two decimals.
func parseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "USD"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "USD"))
	s = strings.TrimPrefix(s, "$")
	if !amountPlain.MatchString(s) && !amountThousands.MatchString(s) {
		return 0, errors.New("not an amount")
	}
	s = strings.ReplaceAll(s, ",", "")
	whole, frac, _ := strings.Cut(s, ".")
	for len(frac) < 2 {
		frac += "0"
	}
	if len(whole) > 12 {
		return 0, errors.New("amount too large")
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, err
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, err
	}
	c := w*100 + f
	if c > maxAmountCents {
		return 0, errors.New("amount too large")
	}
	return c, nil
}

// FormatCents writes us cents as a decimal amount, "100000.00".
func FormatCents(c int64) string {
	return strconv.FormatInt(c/100, 10) + "." + fmt.Sprintf("%02d", c%100)
}
