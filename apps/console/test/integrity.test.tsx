import { describe, expect, it, vi } from "vitest";
import { integrityPath } from "@/api/endpoints";
import { integrityReport, webProject, webSort } from "@/api/schemas";
import {
  dayCountsText,
  evidenceFacts,
  gradeText,
  IntegrityPanel,
  IntegrityReportView,
} from "@/components/integrity";
import { OperatorProjectsPage } from "@/pages/operator/projects";
import * as c from "./fixtures/contract";
import * as f from "./fixtures/global";
import { type Call, click, render, renderRoutes, waitFor } from "./helpers";

const EM_DASH = String.fromCharCode(0x2014);

// a fetch double that answers by path, because a dialog and its panel start
// their two reads in an order react does not promise
function stubByPath(answers: Record<string, unknown>): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit = {}) => {
      calls.push({
        url: input,
        method: init.method ?? "GET",
        headers: new Headers(init.headers),
        body: undefined,
      });
      const path = input.split("?")[0] ?? input;
      const body = answers[path];
      if (body === undefined) throw new Error(`unexpected request ${input}`);
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return calls;
}

describe("contract 17 schemas", () => {
  it("parse a report and refuse a grade that does not exist", () => {
    expect(integrityReport.safeParse(f.integrityReportBody).success).toBe(true);
    const bad = structuredClone(f.integrityReportBody);
    (bad as { grade: string }).grade = "D";
    expect(integrityReport.safeParse(bad).success).toBe(false);
    const unknownReason = structuredClone(f.integrityReportBody);
    const first = unknownReason.reasons[0];
    if (first) (first as { code: string }).code = "made_up";
    expect(integrityReport.safeParse(unknownReason).success).toBe(false);
  });

  it("require the grade on every list row and sort by it", () => {
    const { integrity: _, ...withoutGrade } = f.webProjectA;
    expect(webProject.safeParse(withoutGrade).success).toBe(false);
    expect(webSort.options).toContain("integrity");
  });

  it("build both routes with the range", () => {
    expect(integrityPath("tenant", c.TENANT_ID, { from: "2026-11-01", to: "2026-11-12" })).toBe(
      `/tenants/${c.TENANT_ID}/integrity?from=2026-11-01&to=2026-11-12`,
    );
    expect(integrityPath("operator", c.TENANT_ID, { from: "", to: "" })).toBe(
      `/operator/web/integrity?tenant=${c.TENANT_ID}`,
    );
  });
});

describe("integrity text", () => {
  it("names grades and day counts in words", () => {
    expect(gradeText("A")).toBe("Grade A");
    expect(gradeText(null)).toBe("Not graded");
    expect(dayCountsText(f.integrityReportBody as never)).toBe(
      "2 graded site days: 1 A, 1 B, 0 C.",
    );
    expect(
      dayCountsText({ grade: null, graded_days: 0, days_a: 0, days_b: 0, days_c: 0 }),
    ).toContain("No site day");
  });

  it("states the evidence of a day, network shares from the classified events", () => {
    const day = integrityReport.parse(f.integrityReportBody).sites[0]?.days[0];
    if (!day) throw new Error("no day");
    const facts = Object.fromEntries(evidenceFacts(day).map((x) => [x.label, x.value]));
    expect(facts["Access networks"]).toBe("85%");
    expect(facts["Hosting networks"]).toBe("8.0%");
    expect(facts["Distinct networks"]).toBe("120");
    expect(facts["Proof of work passed"]).toBe("99%");
    expect(facts["Baseline visitors"]).toBe("380 (median of 28 days)");
    const open = integrityReport.parse(f.integrityReportBody).sites[0]?.days[1];
    if (!open) throw new Error("no day");
    const o = Object.fromEntries(evidenceFacts(open).map((x) => [x.label, x.value]));
    expect(o["Access networks"]).toBe("–");
    expect(o["Baseline visitors"]).toBe("–");
  });
});

describe("integrity panel", () => {
  it("shows the grade, the reasons, every site day and the data attribution", async () => {
    const report = integrityReport.parse(f.integrityReportBody);
    const site = report.sites[0]?.site_id ?? "";
    const host = await render(
      <IntegrityPanel report={report} siteNames={{ [site]: "dex.example.org" }} />,
    );
    const text = host.textContent ?? "";
    expect(text).toContain("Grade B");
    expect(text).toContain("A large share of accepted events went to the quarantine band.");
    expect(text).toContain("dex.example.org");
    expect(text).toContain("15% of accepted events went to the quarantine band (limit 10%)");
    expect(text).toContain("day in progress");
    expect(text).toContain("can lower a grade, never raise it");
    const links = [...host.querySelectorAll("a")].map((a) => a.getAttribute("href"));
    expect(links).toContain("https://db-ip.com");
    expect(links).toContain("https://github.com/ipverse/as-metadata");
    expect(text).not.toContain(EM_DASH);
  });

  it("reads the project's own route for a member", async () => {
    const calls = stubByPath({ [`/api/tenants/${c.TENANT_ID}/integrity`]: f.integrityReportBody });
    const host = await render(
      <IntegrityReportView
        scope="tenant"
        tenantId={c.TENANT_ID}
        range={{ from: "", to: "" }}
        intro="This is exactly the report SCF operators see for your project."
      />,
    );
    await waitFor(() => expect(host.textContent).toContain("Grade B"));
    expect(host.textContent).toContain("exactly the report SCF operators see");
    expect(calls.map((x) => x.url)).toEqual([`/api/tenants/${c.TENANT_ID}/integrity`]);
  });
});

describe("appeal this grade", () => {
  it("opens a prefilled issue on the appeal form with no personal data", async () => {
    const report = integrityReport.parse(f.integrityReportBody);
    const site = report.sites[0]?.site_id ?? "";
    const host = await render(
      <IntegrityPanel
        report={report}
        appeal={{ slug: "stellar-dex", siteDomains: { [site]: "dex.example.org" } }}
      />,
    );
    const a = host.querySelector<HTMLAnchorElement>("[data-testid=integrity-appeal]");
    expect(a?.textContent).toBe("Appeal this grade");
    expect(a?.getAttribute("target")).toBe("_blank");
    const url = new URL(a?.getAttribute("href") ?? "");
    expect(`${url.origin}${url.pathname}`).toBe("https://github.com/justmert/veristar/issues/new");
    const q = url.searchParams;
    expect(q.get("template")).toBe("integrity-appeal.yml");
    expect(q.get("title")).toBe(
      `integrity appeal: stellar-dex, ${report.from} to ${report.to}, grade B`,
    );
    expect(q.get("project")).toBe("stellar-dex");
    expect(q.get("period")).toBe(`${report.from} to ${report.to} (UTC days)`);
    expect(q.get("grade")).toBe("B");
    expect(q.get("sites")).toBe(`dex.example.org: ${report.sites[0]?.grade}`);
    expect(q.get("reasons")).toContain("quarantine_share (level B,");
    // the rule set that graded the days, not the one the jobs apply now
    expect(q.get("rule_version")).toBe("1");
    // only these fields; the tenant id and figures of a visitor never go
    expect([...q.keys()].sort()).toEqual([
      "grade",
      "period",
      "project",
      "reasons",
      "rule_version",
      "sites",
      "template",
      "title",
    ]);
    expect(url.toString()).not.toContain(report.tenant_id);
  });

  it("names every rule set that graded a period across a rule change", async () => {
    const report = integrityReport.parse({ ...f.integrityReportBody, rule_versions: [1, 2] });
    const host = await render(
      <IntegrityPanel report={report} appeal={{ slug: "stellar-dex", siteDomains: {} }} />,
    );
    expect(host.textContent).toContain("Graded with rule sets 1 and 2");
    const a = host.querySelector<HTMLAnchorElement>("[data-testid=integrity-appeal]");
    expect(new URL(a?.getAttribute("href") ?? "").searchParams.get("rule_version")).toBe("1, 2");
  });

  it("is not offered for a range without a grade, nor without an appeal context", async () => {
    const report = integrityReport.parse({ ...f.integrityReportBody, grade: null });
    const host = await render(
      <IntegrityPanel report={report} appeal={{ slug: "stellar-dex", siteDomains: {} }} />,
    );
    expect(host.querySelector("[data-testid=integrity-appeal]")).toBeNull();
    const plain = await render(
      <IntegrityPanel report={integrityReport.parse(f.integrityReportBody)} />,
    );
    expect(plain.querySelector("[data-testid=integrity-appeal]")).toBeNull();
  });

  it("is not offered for grade A", async () => {
    const report = integrityReport.parse({ ...f.integrityReportBody, grade: "A" });
    const host = await render(
      <IntegrityPanel report={report} appeal={{ slug: "stellar-dex", siteDomains: {} }} />,
    );
    expect(host.querySelector("[data-testid=integrity-appeal]")).toBeNull();
  });
});

describe("integrity in the global web view", () => {
  it("shows each row's grade, sorts by it and opens the reasons", async () => {
    const calls = stubByPath({
      "/api/operator/web/projects": f.webProjectsBody,
      "/api/operator/web/integrity": f.integrityReportBody,
      [`/api/operator/tenants/${c.TENANT_ID}`]: c.operatorDetailBody,
      "/api/operator/cohorts": f.cohortListBody,
    });
    const host = await renderRoutes(
      [{ path: "/operator/projects", element: <OperatorProjectsPage /> }],
      "/operator/projects?view=web",
    );
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    const row = host.querySelector<HTMLElement>(`tr[data-tenant="${c.TENANT_ID}"]`);
    expect(row?.querySelector('[data-col="integrity"]')?.textContent).toBe("Grade B");
    const other = host.querySelector<HTMLElement>(`tr[data-tenant="${f.TENANT_B}"]`);
    expect(other?.querySelector('[data-col="integrity"]')?.textContent).toBe("Not graded");

    const header = [...host.querySelectorAll("th button")].find((b) => b.textContent === "Grade");
    if (!header) throw new Error("no integrity header");
    await click(header as HTMLElement);
    // the first order of a number is largest first: C before B before A
    await waitFor(() =>
      expect(calls.map((x) => x.url)).toContain(
        "/api/operator/web/projects?sort=integrity&order=desc&limit=50",
      ),
    );

    const cell = row?.querySelector<HTMLElement>('[data-col="integrity"]');
    if (!cell) throw new Error("no integrity cell");
    await click(cell);
    await waitFor(() => expect(host.textContent).toContain("Integrity of Stellar DEX"));
    await waitFor(() =>
      expect(host.textContent).toContain("15% of accepted events went to the quarantine band"),
    );
    expect(calls.map((x) => x.url)).toContain(
      `/api/operator/web/integrity?tenant=${c.TENANT_ID}&from=2026-10-14&to=2026-11-12`,
    );
  });

  it("filters to a grade and below and sends the filter", async () => {
    const calls = stubByPath({
      "/api/operator/web/projects": f.webProjectsBody,
      "/api/operator/cohorts": f.cohortListBody,
    });
    const host = await renderRoutes(
      [{ path: "/operator/projects", element: <OperatorProjectsPage /> }],
      "/operator/projects?view=web&integrity_at_or_below=C",
    );
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    expect(calls.map((x) => x.url)).toContain(
      "/api/operator/web/projects?integrity_at_or_below=C&sort=pageviews&order=desc&limit=50",
    );
    const select = [...host.querySelectorAll("label")].find((l) => l.textContent === "Grade");
    expect(select).toBeDefined();
    const options = [...host.querySelectorAll(`#${CSS.escape(select?.htmlFor ?? "")} option`)].map(
      (o) => o.textContent,
    );
    expect(options).toEqual(["Any", "Any grade given", "B or C", "C"]);
    // the active filter shows as a chip that removes it
    expect(host.querySelector("ul[aria-label='Active filters']")?.textContent).toContain(
      "Grade: C",
    );
  });
});
