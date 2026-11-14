---
title: "Support"
description: "How tenants and everyone else get help, and the response and resolution targets the operator commits to."
---

You get help with Veristar through the repository's public GitHub issue tracker. This page explains
which form to use, the response and resolution targets, and how the targets are tracked. The
targets for availability and security patching are on [Service targets](sla.md).

## The channel: the public issue tracker

Support runs on the issue tracker at
[github.com/justmert/veristar/issues](https://github.com/justmert/veristar/issues). It is the only
support channel: there is no chat, email address or phone line.

Security vulnerabilities are the one exception. Report them privately through GitHub private
vulnerability reporting, never in a public issue ([security policy](security/SECURITY.md)).

The operator of the service answers issues: the team that runs it during the 12-month service
window, and the community-appointed operator after a handover ([handover plan](handover/plan.md)).
The tenant terms point to the same tracker ([terms](compliance/terms.md#10-contact)).

## Opening an issue

Every new issue starts from one of the forms in `.github/ISSUE_TEMPLATE/`. Blank issues are turned
off.

| Form | Use it for | Label |
|---|---|---|
| Bug report | something that does not work as documented | `bug` |
| Tenant support | onboarding, dashboards, tokens, enrollment, members, domain verification | `support` |
| Erasure request help | an erasure request in the console that failed or whose result is unclear | `erasure` |
| Report a security vulnerability (link) | opens GitHub private vulnerability reporting | none, the report is private |

Every form reminds you that the tracker is public and asks you not to paste tokens, keys, cookies,
IP addresses, user agents or session ids. Name your project by its slug.

Erasure is self-service: a project owner erases data on the project's **Privacy** page
([erasure](compliance/erasure.md)). A visitor of a tenant's website asks that website, which is the
controller of the data. Requests that reach the maintainers are forwarded to the tenant
(section 7 of the [DPA template](compliance/dpa-template.md)).

GitHub applies a form's label only if the label exists in the repository. `bug` is one of GitHub's
default labels. A maintainer creates the other two once:

```sh
gh label create support --description "Help for an enrolled project" --color 0e8a16
gh label create erasure --description "Help with an erasure request" --color 5319e7
```

## Targets

| Target | Commitment | Clock starts | Clock stops |
|---|---|---|---|
| Response | within **5 business days** | the issue is opened | the first comment by a maintainer |
| Resolution | within **10 business days** | the issue is opened | the issue is closed |

- **Business days** are Monday to Friday in UTC. A holiday counts as a business day unless it is
  listed in the repository variable `SUPPORT_HOLIDAYS` (comma-separated `YYYY-MM-DD` dates).
- **A maintainer** is an account that GitHub marks as the repository owner, an organisation member
  or a collaborator on the issue or comment (`author_association` `OWNER`, `MEMBER` or
  `COLLABORATOR`), plus any login listed in the repository variable `SUPPORT_MAINTAINERS`. Bot
  comments never count, and an issue's author never answers their own issue.
- **Support issues** are open issues that are not pull requests, not opened by a bot and not opened
  by a maintainer. Issues that the hourly security workflow files (label `security-finding`, opened
  by `github-actions[bot]`) follow the 24-hour patch target on [Service targets](sla.md) instead.
  Issues that maintainers open for their own work are not support issues.
- A target is missed at the exact moment the business time since the issue was opened reaches it.
  A response at or after that moment is late. A reopened issue keeps its original opening time.
- **Resolved** means closed by a maintainer with an answer, a fix, or a stated reason why nothing
  will change. Closing the issue is the event the tooling measures.

## How the targets are tracked

The workflow `.github/workflows/issue-age.yml` runs every hour and calls `cmd/issueage`. The tool
reads every open issue and its comments through the GitHub REST API and labels support issues:

| Label | Applied when |
|---|---|
| `support: response due` | no maintainer response, 4 or more business days since opening |
| `support: response overdue` | 5 business days passed without a response, or the response came later |
| `support: resolution due` | open for 9 or more business days |
| `support: resolution overdue` | open for 10 or more business days |

The tool removes a "due" label once it no longer applies: a response arrives, or the matching
"overdue" label replaces it. It never removes an "overdue" label, so a closed issue keeps the record
of a missed target. The tool creates the four labels the first time it needs them and writes a table
of every open support issue with its deadlines to the workflow run's summary. Because it runs
hourly, a label appears up to one hour after the exact deadline.

To see what needs attention, filter the issue list on a label, for example
`label:"support: response due"`. The number of missed targets in a month is the number of issues
closed that month with an "overdue" label.

### Run the tool by hand

Without `-apply`, the tool reports and changes nothing:

```sh
GITHUB_TOKEN=$(gh auth token) go run ./cmd/issueage -repo justmert/veristar
```

Add `-apply` to change labels, or `-now 2026-11-20T09:00:00Z` to assess the issues at another time.
`go run ./cmd/issueage -h` lists the other flags.

### How the tool is tested

- `cmd/issueage/age_test.go` checks that each label appears exactly when an issue crosses 4, 5, 9
  and 10 business days, and not one second earlier, across weekends and holidays.
- `cmd/issueage/github_test.go` runs the full flow against recorded GitHub REST API answers: listing
  with cursor pagination, comments, label creation, adding and removing labels, and the report. It
  also checks that pull requests, bot issues, maintainers' own issues and bot comments are ignored,
  and that the token is sent to the API host only.

## What support covers

- Integrating the tracker snippet and the OpenTelemetry export, as described in the
  [onboarding guides](onboarding/overview.md).
- Problems with the console, the dashboards, tokens, enrollment, members and domain verification.
- Bugs in the service.

Path normalization rules are the same for every project. To group requests more finely, set
`http.route` in your own middleware ([Setting http.route](onboarding/api/http-route.md)).

Support, like the service itself, is free of charge ([terms](compliance/terms.md#2-no-fees)).
