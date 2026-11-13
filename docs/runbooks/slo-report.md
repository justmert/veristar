# Monthly SLO report

The platform's targets are 99.99% ingestion and 99.90% dashboards (RFP 3.4). They are measured
from outside by the probe: every 30 s the prober sends a web event and an OTLP metric and checks
the console, the Umami host and the machine API, and pushes the results to Gatus on
`status.<an operator input>` as `slo_ingestion`, `slo_dashboard`, `slo_freshness-web` and
`slo_freshness-api` ([../reference/observability.md](../reference/observability.md), "Measures").
Gatus keeps raw results for only about a week (20,160 per endpoint) and hourly uptime for 30
days, which is shorter than a month. So `cmd/sloreport` copies each day's raw results into
`docs/slo/data/<YYYY-MM-DD>.json` and writes the month's report from those files to
`docs/slo/<YYYY-MM>.md`.

## Normal operation (automatic)

The workflow `.github/workflows/slo-report.yml` runs every day at 00:30 UTC:

1. collects yesterday with `go run ./cmd/sloreport collect -gatus "$STATUS_BASE_URL" -day <day> -out docs/slo/data`
   and commits it to the branch `slo/<YYYY-MM>`;
2. on the day after a month's last day, writes the report with
   `go run ./cmd/sloreport report -month <YYYY-MM> -data docs/slo/data -out docs/slo`, commits it and
   opens a pull request `slo report <YYYY-MM>` to `main`.

It needs the repository variable `STATUS_BASE_URL` (`https://status.<an operator input>`); without it the
first step fails with `repository variable STATUS_BASE_URL is not set`.

## Each month (operator)

1. **Review the pull request.** The report states the covered range, partial days, probe gaps,
   missing days, and per measure the results, availability, verdict against the objective,
   coverage and downtime budget used, then every incident (a run of failed results) with its first
   error. Missing days count as unmeasured, never as available.
2. **Explain every incident** that used budget: link it to the alert, runbook or change that caused
   it, in the pull request or the incident record.
3. **Check coverage.** Low coverage means the probe or Gatus was down; it lowers trust in the
   availability number. Investigate a coverage below 99%.
4. **Merge.** The report is public in the repository.

## By hand (when the workflow did not run)

The days must be collected while Gatus still holds them (about a week):

```sh
make build
dist/bin/sloreport collect -gatus https://status.<an operator input> -day <YYYY-MM-DD> -out docs/slo/data
```

`-page-size` defaults to 20160, which must equal `observability_gatus_max_results` in the
observability role. Then, once every day of the month is collected:

```sh
dist/bin/sloreport report -month <YYYY-MM> -data docs/slo/data -out docs/slo
```

Expected: `wrote docs/slo/<YYYY-MM>.md`. `-through <YYYY-MM-DD>` writes a partial report up to a day.

## Exercised

| Date | What ran | Result | Source |
|---|---|---|---|
| 2026-11-13 | this stage: `sloreport collect` against the Gatus of the local observability test stack (`tests/observability`), then `sloreport report` for 2026-11 | report written; its numbers matched a manual calculation from the raw results (plan status, this stage) | plan status, this stage |
| 2026-11-13 | this stage: the report step again from the this stage data (`dist/bin/sloreport report -month 2026-11 -data <this stage data> -out <scratch> -through 2026-11-13`) | `wrote .../2026-11.md`, identical to the this stage report (empty diff); ingestion 236 results, 227 successes, 96.1864%; dashboard 238 results, 95.3782%; from a test stack disturbed by the alert drills and host load, with 12 missing days | this page |

Not yet exercised: collection from a real status page (inputs I4 and I7) and the scheduled
workflow (GitHub Actions fails at start-up on the account). The test-stack numbers are not
availability figures of the platform.
