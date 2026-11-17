import { describe, expect, it } from "vitest";
import { coverageEstimate, integrityReport, webProject, webSort } from "@/api/schemas";
import { CoverageCell, CoverageNote, coverageText } from "@/components/coverage";
import { IntegrityPanel } from "@/components/integrity";
import * as f from "./fixtures/global";
import { render } from "./helpers";

describe("coverage estimate (contract 17.5)", () => {
  it("parses the estimate and refuses a share outside 0 to 1", () => {
    expect(
      coverageEstimate.parse({ script_requests: 200, gpc_requests: 30, unmeasured_share: 0.15 }),
    ).toEqual({ script_requests: 200, gpc_requests: 30, unmeasured_share: 0.15 });
    expect(() =>
      coverageEstimate.parse({ script_requests: 1, gpc_requests: 2, unmeasured_share: 2 }),
    ).toThrow();
    expect(webSort.parse("unmeasured_share")).toBe("unmeasured_share");
    expect(webProject.parse(f.webProjectA).coverage.unmeasured_share).toBe(0.12);
  });

  it("states the share, too few loads, or no loads", () => {
    expect(coverageText({ script_requests: 2400, gpc_requests: 288, unmeasured_share: 0.12 })).toBe(
      "About 12% of visits are not measured because of privacy signals.",
    );
    expect(coverageText({ script_requests: 40, gpc_requests: 3, unmeasured_share: null })).toBe(
      "Too few loads of the tracker file (40 of 100) to estimate the share of visits not measured because of privacy signals.",
    );
    expect(coverageText({ script_requests: 0, gpc_requests: 0, unmeasured_share: null })).toMatch(
      /^No loads of the tracker file/,
    );
  });

  it("shows the note with its counts, and the list cell with a dash below the minimum", async () => {
    const host = await render(
      <div>
        <CoverageNote
          coverage={{ script_requests: 2400, gpc_requests: 288, unmeasured_share: 0.12 }}
        />
        <CoverageCell
          coverage={{ script_requests: 2400, gpc_requests: 288, unmeasured_share: 0.12 }}
        />
        <CoverageCell coverage={{ script_requests: 40, gpc_requests: 3, unmeasured_share: null }} />
      </div>,
    );
    const note = host.querySelector('[data-testid="coverage"]');
    expect(note?.textContent).toContain("About 12% of visits are not measured");
    expect(note?.textContent).toContain(
      "Of 2,400 loads of the tracker file, 288 carried the signal.",
    );
    const cells = [...host.querySelectorAll('[data-col="unmeasured_share"]')];
    expect(cells.map((c) => c.textContent)).toEqual(["12%", "–no estimate"]);
    // the meaning of the dash and the counts are visible text, not a title
    const details = [...host.querySelectorAll('[data-testid="coverage-detail"]')];
    expect(details.map((d) => d.textContent)).toEqual([
      "288 of 2,400 loads with GPC",
      "too few loads, 3 of 40 loads with GPC",
    ]);
    expect(host.querySelector("[title]")).toBeNull();
  });

  it("is part of the integrity panel a project and its operators see", async () => {
    const host = await render(
      <IntegrityPanel report={integrityReport.parse(f.integrityReportBody)} />,
    );
    expect(host.querySelector('[data-testid="coverage"]')?.textContent).toContain(
      "About 12% of visits are not measured because of privacy signals.",
    );
  });
});
