import { describe, expect, it } from "vitest";
import { groupTitle, sentenceCase } from "@/lib/alerts";
import {
  nextSort,
  phoneColumns,
  readView,
  shownColumns,
  statusParams,
  statusQuery,
  switchView,
} from "@/lib/operator-projects";
import { chartState } from "@/pages/operator/compare";

describe("project list views", () => {
  it("reads the view and falls back to the status view", () => {
    expect(readView("web")).toBe("web");
    expect(readView("api")).toBe("api");
    expect(readView("tenants")).toBe("status");
    expect(readView(null)).toBe("status");
  });

  it("keeps the search and the status across views and drops what a view cannot apply", () => {
    const web = new URLSearchParams(
      "view=web&q=dex&status=active&verified=true&integrity_at_or_below=C&sort=visitors&from=2026-10-01&to=2026-10-30",
    );
    const api = switchView(web, "api");
    expect(api.get("view")).toBe("api");
    expect(api.get("q")).toBe("dex");
    expect(api.get("verified")).toBe("true");
    expect(api.get("from")).toBe("2026-10-01");
    // the api list has no grade and no visitors sort
    expect(api.has("integrity_at_or_below")).toBe(false);
    expect(api.has("sort")).toBe(false);
    const status = switchView(web, "status");
    expect(status.toString()).toBe("q=dex&status=active");
    const back = switchView(new URLSearchParams("q=dex&review=true&sort=name"), "web");
    expect(back.toString()).toBe("q=dex&view=web");
  });

  it("reads and writes the status view's address", () => {
    const q = statusQuery(
      new URLSearchParams("status=pending,active&review=true&sort=name&order=asc"),
    );
    expect(q).toEqual({
      status: ["pending", "active"],
      review: true,
      q: "",
      sort: "name",
      order: "asc",
    });
    expect(statusParams(q).toString()).toBe(
      "status=pending%2Cactive&review=true&sort=name&order=asc",
    );
  });

  it("flips the order of the current sort and starts a new one in its natural order", () => {
    expect(nextSort({ sort: "pageviews", order: "desc" }, "pageviews")).toEqual({
      sort: "pageviews",
      order: "asc",
    });
    expect(nextSort({ sort: "pageviews", order: "desc" }, "name")).toEqual({
      sort: "name",
      order: "asc",
    });
    expect(nextSort({ sort: "name", order: "asc" }, "visitors")).toEqual({
      sort: "visitors",
      order: "desc",
    });
  });

  it("always shows the name and the sorted column, and keeps the phone numbers short", () => {
    const shown = shownColumns("web", ["pageviews"], "bounces");
    expect(shown).toEqual(["name", "pageviews", "bounces"]);
    expect(shownColumns("web", null, "pageviews")).toContain("integrity");
    // the status sits in its own column right after the name in every view
    for (const view of ["status", "web", "api"] as const)
      expect(shownColumns(view, null, "name").slice(0, 2)).toEqual(["name", "status"]);
    expect(shownColumns("status", null, "name")).toEqual([
      "name",
      "status",
      "sites",
      "sites_verified",
      "sites_with_stellar_toml",
      "created_at",
      "status_changed_at",
    ]);
    expect(shownColumns("web", ["not-a-column"], "pageviews")).toEqual(["name", "pageviews"]);
    const defaults = shownColumns("web", null, "pageviews");
    expect(phoneColumns("web", defaults, "pageviews")).toEqual([
      "pageviews",
      "visitors",
      "integrity",
    ]);
    // the sorted column takes the third place on a phone
    const sorted = shownColumns("web", null, "bounce_rate");
    expect(phoneColumns("web", sorted, "bounce_rate")).toEqual([
      "pageviews",
      "visitors",
      "bounce_rate",
    ]);
  });
});

describe("compare charts", () => {
  const buckets = [
    { at: "2026-11-16", label: "", tick: "" },
    { at: "2026-11-17", label: "", tick: "" },
    { at: "2026-11-18", label: "", tick: "", partial: true },
  ];

  it("draws only when a complete day has a value", () => {
    expect(chartState(buckets, [[1, null, 3]])).toBe("data");
    expect(chartState(buckets, [[0, 0, 0]])).toBe("data");
    expect(chartState(buckets, [[null, null, 5]])).toBe("no-complete");
    expect(chartState(buckets, [[null, null, null]])).toBe("empty");
  });
});

describe("alert words", () => {
  it("starts titles with a capital and names groups by what to do", () => {
    expect(sentenceCase("the gateway burns its error budget")).toBe(
      "The gateway burns its error budget",
    );
    expect(groupTitle("page")).toBe("Act now");
    expect(groupTitle("ticket")).toBe("Act within a working day");
    expect(groupTitle("")).toBe("No severity");
    expect(groupTitle("info")).toBe("Info");
  });
});
