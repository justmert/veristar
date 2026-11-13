# Support

How tenants and anyone else get help with the service, and the targets the operator commits
to (RFP 3.4). The operational targets for availability and security patching are in
[sla.md](sla.md).

## The channel: the public issue tracker

Support runs on the repository's GitHub issue tracker,
`https://github.com/justmert/stellar-analytics/issues`. The RFP states that a public issue
tracker is sufficient and that no on-call rotation or other public support channel is
required. There is none: no chat, no email address, no phone.

One exception, for security: vulnerabilities are reported privately through GitHub private
vulnerability reporting, never in a public issue ([security/SECURITY.md](security/SECURITY.md)).

Current state, stated plainly: the repository is private until the operator confirms making it
public (plan an operator input, so today only people with access to it can open issues. The tracker
becomes the public support channel when the repository is made public, which must happen
before production launch.

Who answers: during the 12-month service window, the proposer, who must host the system and
provide user support for that time (RFP 3.4). After handover,
the community-appointed operator ([handover/plan.md](handover/plan.md)). The wording of the
support commitment and where tenants are told about it are an open question for SCF (plan
section 27); the terms already point to the tracker (`docs/compliance/terms.md` section 10).

## Opening an issue

New issues start from one of the forms in `.github/ISSUE_TEMPLATE/` (blank issues are
turned off):

| Form | For | Label |
|---|---|---|
| Bug report | something that does not work as documented | `bug` |
| Tenant support | onboarding, dashboards, tokens, enrollment, members, domain verification | `support` |
| Erasure request help | a project owner whose erasure request in the console failed or is unclear | `erasure` |
| Report a security vulnerability (link) | opens GitHub private vulnerability reporting | none, private |

Every form warns that the tracker is public and asks for no tokens, keys, cookies, IP
addresses, user agents or session ids. Projects are named by their slug.

Erasure is self-service: a project owner erases data on the project's Privacy page
([compliance/erasure.md](compliance/erasure.md)). A visitor of a tenant's website asks that
website, which is the controller. Requests that reach the maintainers are forwarded to the
tenant (`docs/compliance/dpa-template.md` section 7).

GitHub adds a form's label only if the label already exists in the repository. `bug` is one
of GitHub's default labels; the other two are created once by a maintainer:

```sh
gh label create support --description "Help for an enrolled project" --color 0e8a16
gh label create erasure --description "Help with an erasure request" --color 5319e7
```

## Targets

| Target | Commitment | Clock starts | Clock stops |
|---|---|---|---|
| Response | within **5 business days** | the issue is opened | the first comment by a maintainer |
| Resolution | within **10 business days** | the issue is opened | the issue is closed |

- **Business days** are Monday to Friday in UTC. Holidays count as business days unless they
  are listed in the repository variable `SUPPORT_HOLIDAYS` (comma-separated `YYYY-MM-DD`).
  None is listed today.
- **A maintainer** is an account GitHub marks as the repository owner, an organisation member
  or a collaborator on the issue or comment (`author_association` `OWNER`, `MEMBER` or
  `COLLABORATOR`), plus any login in the repository variable `SUPPORT_MAINTAINERS`. Bot
  comments never count. The issue's author never answers their own issue.
- **Support issues** are open issues that are not pull requests, not opened by a bot and not
  opened by a maintainer. Issues filed by the hourly security workflow (`security-finding`,
  opened by `github-actions[bot]`) follow the 24-hour patch target in [sla.md](sla.md)
  instead, and issues maintainers open for their own work are not tickets.
- A limit is crossed at the exact moment the business time since the issue was opened
  reaches it. A response at or after the limit is late. A reopened issue keeps its original
  opening time.
- "Resolved" means closed by a maintainer with an answer, a fix, or a stated reason why no
  change will be made. Closing an issue is the resolution event the tooling measures.

## How the targets are watched

The scheduled workflow `.github/workflows/issue-age.yml` runs every hour and calls
`cmd/issueage`, which reads every open issue and its comments through the GitHub REST API and
labels support issues:

| Label | When |
|---|---|
| `support: response due` | no maintainer response, 4 business days or more since opening |
| `support: response overdue` | 5 business days passed without a response, or the response came later |
| `support: resolution due` | open 9 business days or more |
| `support: resolution overdue` | open 10 business days or more |

The "due" labels are removed when they no longer apply (a response arrives, or the "overdue"
label replaces them). The "overdue" labels are never removed by the tool, so closed issues
keep the record of a missed target. The tool creates the four labels the first time it needs
them, and writes a table of every open support issue with its deadlines to the workflow run's
summary. Because it runs hourly, a label can appear up to one hour after the exact crossing.

Maintainers filter on these labels (`label:"support: response due"`) to see what needs
attention. A monthly count of missed targets is the number of issues closed that month with
an "overdue" label.

Run it by hand, without changing anything:

```sh
GITHUB_TOKEN=$(gh auth token) go run ./cmd/issueage -repo justmert/stellar-analytics
```

Add `-apply` to change labels, `-now 2026-11-20T09:00:00Z` to assess at another time, and see
`go run ./cmd/issueage -h` for the other flags.

### What was verified

- The rule that each label appears exactly when an issue crosses 4, 5, 9 and 10 business days
  (and not one second earlier), across weekends and holidays, is tested in
  `cmd/issueage/age_test.go`.
- The full run (listing with cursor pagination, comments, label creation, adding and removing
  labels, the report) is tested against GitHub REST API answers recorded on 2026-11-13, in
  `cmd/issueage/github_test.go`. That test also checks that pull requests, bot issues,
  maintainers' own issues and bot comments are ignored, and that the token is never sent to a
  host other than the API.
- A dry run against the real GitHub API on 2026-11-13 read this repository (no open issues)
  and a public repository with 116 open support issues over two pages, and reported each.
- The workflow itself has **not** run on GitHub: every workflow on this account ends in
  `startup_failure` (plan, this stage status). It needs the operator to fix GitHub Actions for the
  account.

## What support does not cover

- Problems inside a tenant's own website or application beyond integrating the snippet or the
  OpenTelemetry export.
- Custom path normalization rules for a tenant: the rule set is fixed by design, and tenants
  set `http.route` in their own middleware for finer control (RFP 3.3).
- Any charge: the service is free to tenants, SCF and everyone else (RFP 3.1).
