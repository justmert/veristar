import { describe, expect, it } from "vitest";
import { ingest, snippet } from "@/api/schemas";
import { parseBlocks } from "@/lib/markdown";
import { rejectionRows, sumRejected, sumUnclassified } from "@/lib/outcomes";
import { snippetGuides } from "@/lib/snippet-guides";
import { hourSlots } from "@/pages/project/overview";
import { safeReturnTo } from "@/pages/public/sign-in";
import * as f from "./fixtures/contract";

describe("ingest outcomes", () => {
  it("names known reasons and sums unknown ones as other rejections", () => {
    const totals = {
      accepted: 5,
      rejected_origin: 3,
      rejected_gpc: 1,
      rejected_new_a: 2,
      rejected_new_b: 2,
    };
    expect(rejectionRows(totals)).toEqual([
      { key: "other", label: "Other rejections", count: 4 },
      { key: "rejected_origin", label: "Origin not allowed", count: 3 },
      { key: "rejected_gpc", label: "Global Privacy Control", count: 1 },
    ]);
    expect(sumRejected(totals)).toBe(8);
    expect(sumUnclassified({ accepted: 1, future_class: 7 })).toBe(7);
  });

  it("fills hours the route omitted with zero", () => {
    const slots = hourSlots(ingest.parse(f.ingestBody));
    expect(slots).toHaveLength(24);
    expect(slots[0]).toEqual({ at: "2026-11-11T09:00:00Z", accepted: 120, quarantined: 3 });
    expect(slots[1]).toEqual({ at: "2026-11-11T10:00:00Z", accepted: 0, quarantined: 0 });
  });
});

describe("snippet guides", () => {
  it("show the exact tag in every framework tab and hide npm while it is null", () => {
    const s = snippet.parse(f.snippetBody);
    const guides = snippetGuides(s);
    expect(guides.map((g) => g.label)).toEqual([
      "Static HTML",
      "React",
      "Vue",
      "Astro",
      "SvelteKit",
      "Server-rendered",
    ]);
    for (const g of guides) {
      expect(g.copy).toBe(s.tag);
      expect(g.code).toContain(s.tag);
    }
  });

  it("add an npm tab with the api's install command once published", () => {
    const s = snippet.parse({
      ...f.snippetBody,
      npm: {
        package: "tracker",
        version: "1.0.0",
        install: "pnpm add tracker@1.0.0",
        import: 'import "tracker";',
      },
    });
    const npm = snippetGuides(s).at(-1);
    expect(npm?.id).toBe("npm");
    expect(npm?.copy).toBe("pnpm add tracker@1.0.0");
    expect(npm?.code).toContain(
      `init({ siteId: "${s.site_id}", host: "https://in.example.net" });`,
    );
    // consent and the title are init() options for the package (1.3.0 and later read the title)
    const late = snippet.parse({ ...s, version: "1.3.0", npm: { ...s.npm, version: "1.3.0" } });
    const withOpts = snippetGuides(late, { consent: true, title: true }).at(-1);
    expect(withOpts?.code).toContain("autoTrack: false, sendTitle: true });");
    expect(snippetGuides(s, { title: true }).at(-1)?.code).not.toContain("sendTitle");
  });
});

describe("terms markdown", () => {
  it("drops front matter and keeps block structure", () => {
    const blocks = parseBlocks(f.termsBody.markdown);
    expect(blocks.map((b) => b.kind)).toEqual(["h", "quote", "p"]);
  });

  it("groups list items with continuation lines", () => {
    expect(parseBlocks("1. one\n2. two\n   more\n\n- a\n- b")).toEqual([
      { kind: "ol", items: ["one", "two more"] },
      { kind: "ul", items: ["a", "b"] },
    ]);
  });
});

describe("sign-in return path", () => {
  it("keeps console paths and refuses anything a browser reads as another host", () => {
    expect(safeReturnTo("/projects/x?y=1")).toBe("/projects/x?y=1");
    for (const bad of [
      null,
      "",
      "https://evil.example",
      "//evil.example",
      "/\\evil.example",
      `/${"a".repeat(600)}`,
    ])
      expect(safeReturnTo(bad)).toBe("/");
  });
});
