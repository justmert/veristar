---
title: "Branch protection for main"
description: "The repository settings that keep the history of main intact, and the checks that run on every change."
---

These are the settings for `main` in `github.com/justmert/veristar`. They keep the history of
`main` intact: nobody can force-push over it or delete it. Every push to `main` and every pull
request runs the checks listed below, so a change that breaks the build, the tests, the tenant
isolation gate or the supply-chain gates shows up on the commit that introduced it
([threat-model.md](threat-model.md)).

GitHub offers protected branches on public repositories with GitHub Free and GitHub Free for
organizations, and on public and private repositories with GitHub Pro, GitHub Team, GitHub
Enterprise Cloud and GitHub Enterprise Server
([GitHub REST documentation](https://docs.github.com/en/rest/branches/branch-protection)).

## Settings

| Setting | Value | Why |
|---|---|---|
| Allow force pushes | off | History on `main` cannot be rewritten |
| Allow deletions | off | `main` cannot be deleted |

## Checks on every change

GitHub names a check run after the job's `name:`, or after the job id when the job has no
`name:`. None of the jobs below sets `name:`, so the check names are the job ids.

The jobs in `.github/workflows/*.yml`:

| Workflow file | Job (check name) | Runs on |
|---|---|---|
| `ci.yml` | `go` | every push to `main` and every pull request. `make test`: unit tests, no containers |
| `ci.yml` | `containers` | every push and pull request. `make test-containers`: the packages whose tests start PostgreSQL, NATS, Umami or Caddy |
| `ci.yml` | `js` | every push and pull request |
| `ci.yml` | `integration` | every push and pull request |
| `ci.yml` | `image` | every push and pull request |
| `ci.yml` | `gitleaks` | every push and pull request |
| `ci.yml` | `licences` | every push and pull request |
| `ci.yml` | `isolation` | every push and pull request. The tenant isolation gate (`make isolation`) |
| `ci.yml` | `supplychain` | every push and pull request |
| `ci.yml` | `actionlint` | every push and pull request. The workflows themselves, with shellcheck on every `run:` block |
| `ci.yml` | `tofu` | every push and pull request. `make tofu-check`: OpenTofu format, validate and the mocked inventory tests |
| `ci.yml` | `ansible` | every push and pull request. `make ansible-lint`: yamllint and ansible-lint (production profile) |
| `ci.yml` | `promtool` | every push and pull request. `make promtool`: alert rule syntax and the promtool rule tests |
| `tracker.yml` | `releases` | every push and pull request |
| `tracker.yml` | `filter-lists` | every push and pull request, and daily. It downloads the current filter lists, so a list change can fail it without a code change. That is intended: the endpoint names must stay unblocked by ad blockers. |
| `release-images.yml` | `caddy`, `images`, `manifest` | tags `v*` |
| `release-tracker.yml` | `prove`, `publish` | tags `tracker-v*` |
| `security.yml` | `advisories`, `images`, `go`, `npm`, `report` | hourly schedule and on demand |
| `slo-report.yml` | `collect` | schedule and on demand |
| `e2e.yml` | `suite` (one matrix job per end-to-end and container suite, named after the suite) | nightly schedule and on demand |
| `stellartoml-live.yml` | `live` | schedule and on demand |
| `issue-age.yml` | `label` | hourly schedule and on demand |

## Command to apply

Run this with a `gh` token that has admin rights on the repository. The body follows
`PUT /repos/{owner}/{repo}/branches/{branch}/protection`, where `required_status_checks`,
`enforce_admins`, `required_pull_request_reviews` and `restrictions` are required fields.

```bash
gh api --method PUT \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  repos/justmert/veristar/branches/main/protection \
  --input - <<'EOF'
{
  "required_status_checks": null,
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

Check the result:

```bash
gh api repos/justmert/veristar/branches/main/protection
```
