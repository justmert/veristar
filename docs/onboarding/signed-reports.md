---
title: "Share a signed report"
sidebarTitle: "Signed reports"
description: "Create a signed usage report of your project for a period, share it by link, and let anyone verify it."
---

A signed usage report states your project's usage for a period: web traffic, API requests,
on-chain activity, your integrity grade with its reasons, your SCF cohort and your percentiles in
it. Veristar signs it with its published Stellar key, so a reviewer can check that the numbers
come from Veristar unchanged without an account and without trusting your copy. Attach the link
or the file to an SCF submission instead of assembling screenshots.

## Create a report

1. Open your project and choose **Signed reports**.
2. Pick the period. A report covers whole UTC days that have ended, up to yesterday, at most
   366 days.
3. Choose **Generate and sign**.

Any member of the project who accepted the current terms creates reports. A project can create 20 reports per 24 hours.
The report holds the numbers "What SCF sees" shows for the same period, and the versions of
every definition behind them.

## Share it

Every report has its own link, `https://app.analytics.example/r/<id>`, with an id nobody can
guess. Anyone with the link sees the report and needs no account. The page checks the signature
in the visitor's browser and shows whether it is valid and which of Veristar's keys signed it,
and offers the signed file as JSON.

The **Signed reports** page lists your reports with their link and how often each link was opened. A
report never changes after it is created. To stop sharing one, choose **Revoke link**: the link
then answers "not available" for everyone, and revoking cannot be undone. Your members still see
the revoked report on the **Signed reports** page. Creating a report, revoking its link and every view
of the link are in the audit log; a view records no trait of the person who opened it.

## Your cohort

Your report and your "What SCF sees" page show your percentiles inside your SCF cohort: the
projects in your program and round. An owner of the project proposes your program, round,
category and award date at enrollment or on the "What SCF sees" page; an SCF operator confirms
them. Until then your
percentiles are among all enrolled projects.

## Verify a report

Open its link, or download the JSON and run:

```sh
platformctl report verify report.json
```

Any SEP-53 implementation verifies it too. The format, the canonical JSON form and examples in
Node and Python are in [signed usage reports](../reference/usage-reports.md).

## What a report contains

Aggregates only: daily and total counts, rates, the grade and its reasons, your cohort and
percentiles. No visitor, address, user agent or member of your project is in a report.
