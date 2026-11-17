// the "appeal this grade" link: a new issue in the public tracker, opened
// on the integrity appeal form with every field filled from the report.
// it carries the project slug, the site domains, the period, the grades,
// the reason codes and the rule set version, all of which the operator
// views already show; nothing about a person or a visitor goes in.

import type { IntegrityGrade, IntegrityReport } from "@/api/schemas";

export const APPEAL_REPOSITORY = "https://github.com/justmert/veristar";
export const APPEAL_TEMPLATE = "integrity-appeal.yml";

function gradeOf(g: IntegrityGrade | null): string {
  return g === null ? "not graded" : g;
}

// the issue url; siteDomains maps a site id to its domain
export function appealUrl(
  slug: string,
  report: IntegrityReport,
  siteDomains: Record<string, string>,
): string {
  const period = `${report.from} to ${report.to} (UTC days)`;
  const sites = report.sites
    .map((s) => `${siteDomains[s.site_id] ?? s.site_id}: ${gradeOf(s.grade)}`)
    .join("\n");
  const reasons = report.reasons.length
    ? report.reasons
        .map((r) => `${r.code} (level ${r.level}, ${r.days} ${r.days === 1 ? "day" : "days"})`)
        .join("\n")
    : "none";
  const q = new URLSearchParams({
    template: APPEAL_TEMPLATE,
    title: `integrity appeal: ${slug}, ${report.from} to ${report.to}, grade ${gradeOf(report.grade)}`,
    project: slug,
    period,
    grade: gradeOf(report.grade),
    sites: sites || "none",
    reasons,
    // the versions that graded the period, which an appeal is about; the
    // current version when no day was graded
    rule_version: (report.rule_versions.length ? report.rule_versions : [report.rule_version]).join(
      ", ",
    ),
  });
  return `${APPEAL_REPOSITORY}/issues/new?${q.toString()}`;
}
