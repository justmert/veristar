# Operator guide

For the SCF operators who govern the shared service: who may enroll, which projects stay,
and how projects are compared. Operators see aggregates only. They are not members of any
project and get no access to a project's own web dashboard.

| Page | Covers |
|---|---|
| [review-and-enrollment.md](review-and-enrollment.md) | The review queue, approving and rejecting projects and sites, suspending and resuming, and the switch between open and approval-required enrollment |
| [removal.md](removal.md) | Removing a project that is not part of the ecosystem, what is deleted, and the setting that keeps or deletes its aggregates |
| [global-views.md](global-views.md) | The Global web, Global API and Compare pages: ranges, filters, sorting and side-by-side comparison |
| [reading-the-numbers.md](reading-the-numbers.md) | What bands, daily-unique visitors and span-derived API data mean, and how to read them fairly across projects |
| [api-keys.md](api-keys.md) | Keys for machine consumers of the metrics API: create, use, revoke, usage |

## Becoming an operator

Operators are the GitHub accounts whose numeric ids are listed in `OPERATOR_GITHUB_IDS`
(plan an operator input. In a deployment the value is the Ansible variable `sa_operator_github_ids`
(`docs/deploy/ansible.md`), a comma-separated list of numbers such as `1234567,7654321`.

- The control plane reads the list at start and at every sign-in
  (`docs/reference/control-plane-api.md` section 5.1). Adding an id takes effect when that
  person signs in. Removing an id takes effect at that person's next sign-in or the next
  control plane start, whichever comes first, so restart the control plane when access must
  end at once.
- Every change of operator status is in the audit log as `user.operator_sync`.
- In the console, operators see an **Operator** link. Anyone else who opens `/operator` sees
  "Operators only", and the control plane answers every operator route with
  `403 operator_required` regardless of what the console shows.

The numeric id of a GitHub account is the `id` field of `https://api.github.com/users/<login>`.

## The operator area

The console's operator area has eight tabs, all at `https://app.<BASE_DOMAIN>/operator`:

| Tab | What it does |
|---|---|
| Review queue | Projects waiting for approval, and active projects with a site in review, oldest first |
| Projects | Every project with search, status filter and sort; opens the project detail with its actions |
| Global web | Web numbers of every project over a range, with filters and sort |
| Global API | API numbers of every project over a range, with filters and sort |
| Compare | Up to five projects side by side, web and API |
| API keys | Keys for machine access to the metrics API |
| Audit log | Every recorded change, filterable by project, action, actor, target and time |
| Settings | Enrollment mode and whether removed projects keep their aggregates |

Every operator action requires a reason where the table in
[review-and-enrollment.md](review-and-enrollment.md) says so, is written to the audit log
with the operator's id, and the reason is shown to the project's members.
