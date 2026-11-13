// Command issueage flags support issues that approach or cross the support
// targets: a maintainer response within 5 business days and resolution
// within 10 (docs/support.md, rfp 3.4). it labels open issues on github and
// prints a markdown report; the scheduled workflow issue-age.yml runs it.
//
//	issueage -repo owner/name [-apply] [-now RFC3339] [-response-days 5]
//	         [-resolution-days 10] [-warn-days 1] [-holidays YYYY-MM-DD,...]
//	         [-maintainers login,...] [-api https://api.github.com]
//
// the token comes from GITHUB_TOKEN. without -apply nothing is changed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	hc := &http.Client{Timeout: 30 * time.Second}
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, hc, time.Now)
	stop()
	os.Exit(code)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer,
	hc *http.Client, clock func() time.Time) int {
	fs := flag.NewFlagSet("issueage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", getenv("GITHUB_REPOSITORY"), "repository, owner/name (default $GITHUB_REPOSITORY)")
	api := fs.String("api", "https://api.github.com", "github rest api base url")
	apply := fs.Bool("apply", false, "change labels; without it the run only reports")
	nowFlag := fs.String("now", "", "RFC 3339 time to assess at instead of the current time")
	resp := fs.Int("response-days", 5, "business days to the first maintainer response")
	reso := fs.Int("resolution-days", 10, "business days to resolution (closing the issue)")
	warn := fs.Int("warn-days", 1, "business days before a limit at which an issue is flagged as due")
	hol := fs.String("holidays", "", "comma-separated utc days (YYYY-MM-DD) that are not business days")
	maint := fs.String("maintainers", "", "comma-separated logins that count as maintainers besides owners, members and collaborators")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *repo == "" {
		_, _ = fmt.Fprintln(stderr, "issueage: -repo owner/name is required (or GITHUB_REPOSITORY)")
		return 2
	}
	now := clock().UTC()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "issueage: -now: %v\n", err)
			return 2
		}
		now = t.UTC()
	}
	cal, err := NewCalendar(splitList(*hol))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "issueage: -holidays: %v\n", err)
		return 2
	}
	pol := Policy{ResponseDays: *resp, ResolutionDays: *reso, WarnDays: *warn, Calendar: cal}
	if err := pol.Validate(); err != nil {
		_, _ = fmt.Fprintf(stderr, "issueage: %v\n", err)
		return 2
	}
	token := getenv("GITHUB_TOKEN")
	if *apply && token == "" {
		_, _ = fmt.Fprintln(stderr, "issueage: -apply needs GITHUB_TOKEN")
		return 2
	}
	m := Maintainers{Extra: map[string]bool{}}
	for _, l := range splitList(*maint) {
		m.Extra[strings.ToLower(l)] = true
	}
	c, err := NewClient(*api, *repo, token, hc)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "issueage: %v\n", err)
		return 2
	}
	if err := check(ctx, c, pol, m, now, *apply, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "issueage: %v\n", err)
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden && *apply {
			_, _ = fmt.Fprintln(stderr, "issueage: labelling needs a token with issues: write")
		}
		return 1
	}
	return 0
}

// check reads every open issue, assesses the support tickets among them and,
// with apply, brings the tool's labels in line. it stops at the first api
// error, so a failed run never reports a partial result as complete.
func check(ctx context.Context, c *Client, pol Policy, m Maintainers, now time.Time, apply bool, w io.Writer) error {
	issues, err := c.OpenIssues(ctx)
	if err != nil {
		return err
	}
	var as []Assessment
	for _, i := range issues {
		if !m.IsTicket(i) {
			continue
		}
		t := Ticket{Number: i.Number, Title: i.Title, URL: i.HTMLURL, CreatedAt: i.CreatedAt}
		for _, l := range i.Labels {
			t.Labels = append(t.Labels, l.Name)
		}
		if i.Comments > 0 {
			cs, err := c.Comments(ctx, i.Number)
			if err != nil {
				return err
			}
			t.FirstResponse = m.FirstResponse(i.User.Login, cs)
		}
		as = append(as, pol.Assess(t, now))
	}

	var created []string
	if apply {
		need := map[string]bool{}
		for _, a := range as {
			add, _ := a.Changes()
			for _, l := range add {
				need[l] = true
			}
		}
		for _, spec := range pol.Labels() {
			if !need[spec.Name] {
				continue
			}
			ok, err := c.EnsureLabel(ctx, spec)
			if err != nil {
				return err
			}
			if ok {
				created = append(created, spec.Name)
			}
		}
		for _, a := range as {
			add, remove := a.Changes()
			if len(add) > 0 {
				if err := c.AddLabels(ctx, a.Number, add); err != nil {
					return err
				}
			}
			for _, l := range remove {
				if err := c.RemoveLabel(ctx, a.Number, l); err != nil {
					return err
				}
			}
		}
	}
	return writeReport(w, pol, now, as, apply, created)
}

func when(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func stateText(s State, deadline time.Time) string {
	switch s {
	case StateMet:
		return "met"
	case StateOverdue:
		return "**overdue** (limit " + when(deadline) + ")"
	case StateDue:
		return "due by " + when(deadline)
	}
	return "by " + when(deadline)
}

func cellText(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s)
}

func writeReport(w io.Writer, pol Policy, now time.Time, as []Assessment, apply bool, created []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "## Support issue ages at %s\n\n", when(now))
	hol := "none"
	if h := pol.Calendar.Holidays(); len(h) > 0 {
		hol = strings.Join(h, ", ")
	}
	fmt.Fprintf(&b, "Targets: first maintainer response within %d business days, resolution within %d. "+
		"Issues are flagged %d business day(s) before a limit. Business days are Monday to Friday in UTC; holidays: %s.\n\n",
		pol.ResponseDays, pol.ResolutionDays, pol.WarnDays, hol)
	if len(as) == 0 {
		b.WriteString("No open support issues.\n")
	} else {
		b.WriteString("| Issue | Opened | Business days open | Response | Resolution | Labels added | Labels removed |\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		for _, a := range as {
			add, remove := a.Changes()
			fmt.Fprintf(&b, "| [#%d](%s) %s | %s | %.2f | %s | %s | %s | %s |\n",
				a.Number, a.URL, cellText(a.Title), when(a.CreatedAt), a.Elapsed.Hours()/24,
				stateText(a.Response, a.ResponseDeadline), stateText(a.Resolution, a.ResolutionDeadline),
				cellText(strings.Join(add, ", ")), cellText(strings.Join(remove, ", ")))
		}
	}
	if !apply {
		b.WriteString("\nDry run: no label was changed.\n")
	} else if len(created) > 0 {
		fmt.Fprintf(&b, "\nCreated labels: %s.\n", strings.Join(created, ", "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}
