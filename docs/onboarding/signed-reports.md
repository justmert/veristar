---
title: "Share a signed report"
sidebarTitle: "Signed reports"
description: "Create a signed usage report of your project for a period, share it by link, and let anyone verify it."
---

A signed usage report states your project's usage for a period: web traffic, API requests,
on-chain activity including the calls per function of your verified contracts, your integrity grade with its reasons, your SCF cohort and your percentiles in
it. Veristar signs it with its published Stellar key, so a reviewer can check that the numbers
come from Veristar unchanged without an account and without trusting your copy. Attach the link
or the file to an SCF submission instead of assembling screenshots.

## Create a report

1. Open your project and choose **SCF review**. The reports are in its **Signed reports**
   section, which also lists what a report contains.
2. Pick the period: enter the first and the last day as `2026-11-18`. A report covers whole UTC days, at most 366 days, and ends four days before
   today at the latest: Veristar reads today and the three days before it again to count late
   events and grade them, so only older days are final, and a signed number never changes.
3. Choose **Generate and sign**.

Any member of the project creates reports, once an owner has accepted the current terms of service
for the project. A project creates at most 20 reports per 24 hours.

The report holds the numbers **SCF review** shows for the same period, including the estimate of
visits not measured because of privacy signals, the API flags (no data, lossy, infrastructure
requests left out), the weekly active accounts and the calls per function of each verified
contract, and the versions of every definition behind them, including every integrity rule version
that graded the period. Reports use format version 3; reports created in an earlier format keep
their layout and still verify.

## Share it

Every report has its own link, `https://app.analytics.example/r/<id>`, with an id nobody can
guess. Anyone with the link sees the report and needs no account. The page checks the signature
in the visitor's browser and shows whether it is valid and which of Veristar's keys signed it,
and offers the signed file as JSON.

The **Signed reports** section lists your reports with how often each link was opened. **Copy link**
copies a report's link; its menu opens the report, downloads the JSON or revokes the link. A
report never changes after it is created. To stop sharing one, choose **Revoke link**: the link
then answers "not available" for everyone, and revoking cannot be undone. Members of your project
still see the revoked report in the **Signed reports** section. Creating a report, revoking its link and every view
of the link are in the audit log; a view records no trait of the person who opened it.

## Your cohort

Your report and your **SCF review** page show your percentiles inside your SCF cohort: the
projects in your program and round. An owner of the project proposes your program, round,
category and award date at enrollment or on the **SCF review** page; an SCF operator confirms
them. Until then your
percentiles are among all enrolled projects. A percentile shows only in a cohort of 5 projects or
more; in a smaller cohort one project more or less moves it too far, so the page and the report page
show why it is missing instead (the signed JSON file keeps the value).

## Verify a report

Open its link, or download the JSON and run:

```sh
platformctl report verify report.json
```

It checks the signature and that the signer is listed in Veristar's own stellar.toml, whose address
is built into `platformctl`; it never trusts a key or an address the report names.

Any SEP-53 implementation verifies it too. The format, the canonical JSON form and examples in
Node and Python are in [signed usage reports](../reference/usage-reports.md).

## What a report contains

Aggregates only: daily and total counts, rates, the grade and its reasons, your cohort and
percentiles, and the public ids of your verified contracts with their call counts. No visitor,
visitor address, user agent or member of your project is in a report.
