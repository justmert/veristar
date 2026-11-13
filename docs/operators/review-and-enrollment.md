# Review and enrollment modes

The RFP asks for self-service enrollment open to any project built on Stellar, for operators
to be able to remove projects that are not part of the ecosystem, and for a later switch to
approval-required enrollment (RFP 3.1). All three exist. The contract is
`docs/reference/control-plane-api.md` sections 4.2.1 and 5.

## Enrollment modes

The mode is on the operator **Settings** tab, under **Enrollment mode**, or through
`GET` and `PUT /api/operator/settings/enrollment-mode` with `{"mode": "open"}` or
`{"mode": "approval"}`.

| Mode | A new project | Its sites |
|---|---|---|
| `open` (the seeded default) | becomes `active` at once | are set up in the web dashboard and accepted by the gateway as soon as the reconciler has run |
| `approval` | is created `pending` and appears in the review queue | get nothing: no web dashboard, no gateway registry entry. The gateway refuses their events as `rejected_unknown_site`, and the dashboard link answers that set-up is pending |

- The mode is read at each enrollment, so a switch applies to the next enrollment without a
  restart.
- A switch never changes existing projects. Projects that are `active` stay active when the
  mode becomes `approval`.
- Every switch is in the audit log as `settings.enrollment_mode` with the old and new mode.
  Setting the mode it already has writes nothing.

Enrollment itself asks the project for its website, how it uses Stellar, optional Stellar
account or contract ids and an optional SCF round link, and the acceptance of the current
terms (`docs/compliance/terms.md`). A user can create at most 10 projects per day.

## The review queue

The **Review queue** tab lists, oldest first:

- projects with status `pending` (enrolled while the mode was `approval`, or resubmitted after
  a rejection);
- `active` projects with a site in review. A site is put in review when its domain equals, is
  a subdomain of, or is a parent of the domain of a site of a removed project, even in `open`
  mode. This stops a removed project from coming back under a new name with the same domain.

Open a project to see what it entered at enrollment, its members (GitHub logins, to contact
them), its sites with their allowed origins, and its gateway outcomes for the last 24 full
hours and the last 30 full days.

### Evidence shown for each site

Two signals help to judge whether a project is built on Stellar. Neither decides anything,
and neither gates ingestion.

- **Domain verification.** The project proves control of a site's domain with a DNS TXT
  record or a file under `/.well-known/` (contract 5.8). A site shows `verified`,
  `unverified` (with the last error) or `unchecked`. The `jobs` task `verify-domains`
  re-checks every site daily.
- **stellar.toml.** The service fetches `https://<domain>/.well-known/stellar.toml` at site
  creation and daily, and shows whether it was found, the network passphrase it names, how
  many accounts and currencies it lists and whether their keys are valid strkeys
  (contract 5.12). No free text from the file is stored or shown.

The enrollment's Stellar ids are re-checked when the project is opened and shown with their
type (account, muxed account, contract) and whether they are valid.

Which source defines "built on Stellar" is an open question for SCF. Until
it is answered, the decision is the operator's judgement on this evidence.

## Actions on a project

On the project page the console shows only the actions that are valid from the current
status. Each one asks for confirmation.

| Action | From | To | Reason | Effect |
|---|---|---|---|---|
| Approve | `pending` | `active` | optional | every pending site becomes active; dashboards are set up and the sites start receiving traffic |
| Reject | `pending` | `rejected` | required | nothing is set up; the owners see the reason and can edit and resubmit, which puts the project back in the queue as `pending` |
| Suspend | `active` | `suspended` | required | new events from the project's sites are refused and its OTLP tokens stop working; its dashboards stay readable; members can read but cannot add or change sites |
| Resume | `suspended` | `active` | optional | events and OTLP data are accepted again |
| Remove | any status except `removed` | `removed` | required | see [removal.md](removal.md) |

- Any other combination is refused with `409 invalid_transition`, and so is repeating an
  action on a project already in the target state, so a double click never writes two audit
  rows.
- The reason is stored on the project and shown to its members in a banner on every project
  page. Approve and resume clear it.
- Each action writes one audit row (`tenant.approve`, `tenant.reject`, `tenant.suspend`,
  `tenant.resume`, `tenant.remove`) with the old and new status and the reason.

## Actions on a site in review

A site in review shows **In review** on the project page, with **Approve site** and
**Reject site**. Approving makes the site active and it starts receiving traffic. Rejecting
requires a reason, sets the site to `removed` and shows the reason to the owners. Sites of a
`pending` project are not approved one by one; they are approved with the project. Audit
actions: `site.approve`, `site.reject`, and `site.review_queue` when the site entered review.

## Finding projects

The **Projects** tab searches name, slug, website and site domains, filters by status, and
sorts by enrollment date, last status change or name. Through the API the same list is
`GET /api/operator/tenants`, and `review=true` gives the review queue.

## Checking what happened

The **Audit log** tab filters by project id, action (exact, or a prefix ending in a dot such
as `tenant.`), actor (`user:<id>` or `system:<task>`), target and a time range of at most
400 days. The same list is `GET /api/operator/audit`.
