---
title: "Branch protection for main"
description: "The repository settings that make every change go through a reviewed pull request with passing checks."
---

These are the settings for `main` in `github.com/justmert/veristar`. They make every change go
through a reviewed pull request with passing checks. That review is the control that stops a
single compromised account or a careless push from changing the tracker, the Caddy deny lists or
the infrastructure code ([threat-model.md](threat-model.md)).

GitHub offers protected branches on public repositories with GitHub Free and GitHub Free for
organizations, and on public and private repositories with GitHub Pro, GitHub Team, GitHub
Enterprise Cloud and GitHub Enterprise Server
([GitHub REST documentation](https://docs.github.com/en/rest/branches/branch-protection)).

## Required settings

| Setting | Value | Why |
|---|---|---|
| Require a pull request before merging | on | No direct pushes to `main` |
| Required approving reviews | 1 | A second person reads every change |
| Dismiss stale approvals when new commits are pushed | on | An approval covers the code that is merged |
| Require review from code owners | off | The repository has no `CODEOWNERS` file. Turn this on if one is added. |
| Require approval of the most recent push | on | The last pusher cannot approve their own final change |
| Require status checks to pass | on, branch must be up to date (`strict`) | Checks ran against the code that is merged |
| Required checks | listed below | |
| Require conversation resolution | on | Review comments are not lost |
| Require linear history | on | No merge commits; history is easy to audit |
| Allow force pushes | off | History on `main` cannot be rewritten |
| Allow deletions | off | `main` cannot be deleted |
| Include administrators (`enforce_admins`) | on | An administrator account is the most valuable one to compromise |
| Require signed commits | optional | Not required: it adds friction for contributors and the review and checks already gate every change. Turn on with the second command below if wanted. |
| Push restrictions | none | User, team and app restrictions are only available for organization-owned repositories |

With one approving review required and administrators included, every merge needs a second person
with write access. Add at least one such reviewer before you apply these settings; otherwise
nobody can change `main`.

## Required status checks

GitHub names a check run after the job's `name:`, or after the job id when the job has no
`name:`. None of the jobs below sets `name:`, so the check names are the job ids. Only jobs that
run on `pull_request` can be required; a job that runs only on tags or a schedule never reports
on a pull request and would block every merge.

The jobs in `.github/workflows/*.yml`:

| Workflow file | Job (check name) | Runs on pull requests | Required |
|---|---|---|---|
| `ci.yml` | `go` | yes | yes. `make test`: unit tests, no containers |
| `ci.yml` | `containers` | yes | yes. `make test-containers`: the packages whose tests start PostgreSQL, NATS, Umami or Caddy |
| `ci.yml` | `js` | yes | yes |
| `ci.yml` | `integration` | yes | yes |
| `ci.yml` | `image` | yes | yes |
| `ci.yml` | `gitleaks` | yes | yes |
| `ci.yml` | `licences` | yes | yes |
| `ci.yml` | `isolation` | yes | yes. The tenant isolation gate (`make isolation`) |
| `ci.yml` | `supplychain` | yes | yes |
| `ci.yml` | `actionlint` | yes | yes. The workflows themselves, with shellcheck on every `run:` block |
| `ci.yml` | `tofu` | yes | yes. `make tofu-check`: OpenTofu format, validate and the mocked inventory tests |
| `ci.yml` | `ansible` | yes | yes. `make ansible-lint`: yamllint and ansible-lint (production profile) |
| `ci.yml` | `promtool` | yes | yes. `make promtool`: alert rule syntax and the promtool rule tests |
| `tracker.yml` | `releases` | yes | yes |
| `tracker.yml` | `filter-lists` | yes (also daily) | yes. It downloads the current filter lists, so a list change can fail it without a code change. That is intended: the endpoint names must stay unblocked by ad blockers. |
| `release-images.yml` | `caddy`, `images`, `manifest` | no (tags `v*` only) | no |
| `release-tracker.yml` | `prove`, `publish` | no (tags `tracker-v*` only) | no |
| `security.yml` | `advisories`, `images`, `go`, `npm`, `report` | no (hourly schedule and manual) | no |
| `slo-report.yml` | `collect` | no (schedule and manual) | no |
| `e2e.yml` | `suite` (one matrix job per end-to-end and container suite, named after the suite) | no (nightly schedule and manual) | no |
| `stellartoml-live.yml` | `live` | no (schedule and manual) | no |
| `issue-age.yml` | `label` | no (hourly schedule and manual) | no |

## Command to apply

Run this with a `gh` token that has admin rights on the repository. The body
follows `PUT /repos/{owner}/{repo}/branches/{branch}/protection`: `required_status_checks`,
`enforce_admins`, `required_pull_request_reviews` and `restrictions` are required fields;
`dismissal_restrictions` is omitted because it is only available for organization-owned
repositories; `restrictions` is `null` for the same reason.

```bash
gh api --method PUT \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  repos/justmert/veristar/branches/main/protection \
  --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "checks": [
      { "context": "go" },
      { "context": "containers" },
      { "context": "js" },
      { "context": "integration" },
      { "context": "image" },
      { "context": "gitleaks" },
      { "context": "licences" },
      { "context": "isolation" },
      { "context": "supplychain" },
      { "context": "actionlint" },
      { "context": "tofu" },
      { "context": "ansible" },
      { "context": "promtool" },
      { "context": "releases" },
      { "context": "filter-lists" }
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false,
    "required_approving_review_count": 1,
    "require_last_push_approval": true
  },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "block_creations": false,
  "required_conversation_resolution": true,
  "lock_branch": false,
  "allow_fork_syncing": false
}
EOF
```

Optional, signed commits:

```bash
gh api --method POST \
  -H "Accept: application/vnd.github+json" \
  repos/justmert/veristar/branches/main/protection/required_signatures
```

Check the result:

```bash
gh api repos/justmert/veristar/branches/main/protection
```

## Keeping this current

When a workflow job that runs on pull requests is added, renamed or given a `name:`, update the
table and the `checks` list above, then apply the command again. A required check that no longer
exists blocks every pull request.
