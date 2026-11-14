import { describe, expect, it } from "vitest";
import { ingest, snippet } from "@/api/schemas";
import { ingestBuckets, niceTicks } from "@/components/web-charts";
import { CONSENT_ATTR, consentTag, snippetGuides } from "@/lib/snippet-guides";
import { firstEvent } from "@/pages/sites";
import * as f from "./fixtures/contract";

const answer = (totals: Record<string, number>) => ingest.parse({ ...f.ingestBody, totals });

describe("web chart buckets", () => {
  it("cuts an hour window into one bucket per hour", () => {
    const b = ingestBuckets(ingest.parse(f.ingestBody));
    expect(b).toHaveLength(24);
    expect(b[0]?.at).toBe("2026-11-11T09:00:00Z");
    expect(b.some((x) => x.partial)).toBe(false);
  });

  it("marks the first and last utc day of a 30 day window as partial", () => {
    const b = ingestBuckets({
      from: "2026-10-15T17:00:00Z",
      to: "2026-11-14T17:00:00Z",
      bucket: "day",
    });
    expect(b).toHaveLength(31);
    expect(b[0]).toMatchObject({ at: "2026-10-15T00:00:00Z", partial: true });
    expect(b[1]?.partial).toBe(false);
    expect(b[30]).toMatchObject({ at: "2026-11-14T00:00:00Z", partial: true });
  });

  it("chooses 3 to 5 round ticks from zero", () => {
    expect(niceTicks(0)).toEqual([0, 1]);
    expect(niceTicks(1137)).toEqual([0, 500, 1000, 1500]);
    expect(niceTicks(58)).toEqual([0, 20, 40, 60]);
  });
});

describe("first event", () => {
  it("waits, sees refusals, and confirms counted events", () => {
    expect(firstEvent(undefined)).toBe("waiting");
    expect(firstEvent(answer({}))).toBe("waiting");
    expect(firstEvent(answer({ rejected_origin: 4 }))).toBe("refused");
    expect(firstEvent(answer({ rejected_origin: 4 }), answer({ quarantined: 1 }))).toBe("received");
  });
});

describe("consent mode", () => {
  it("adds the auto-track attribute and keeps src and integrity untouched", () => {
    const s = snippet.parse(f.snippetBody);
    const tag = consentTag(s.tag);
    expect(tag).toBe(s.tag.replace("></script>", ` ${CONSENT_ATTR}></script>`));
    expect(tag).toContain(`integrity="${s.integrity}"`);
    for (const g of snippetGuides(s, { consent: true })) {
      expect(g.copy).toBe(tag);
      expect(g.code).toContain(tag);
    }
  });
});
