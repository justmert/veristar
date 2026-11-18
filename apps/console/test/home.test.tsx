import { describe, expect, it } from "vitest";
import type { MeTenant } from "@/api/schemas";
import { EnrollForm } from "@/pages/home/enroll";
import { matchProjects, nextStep } from "@/pages/home/projects";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, render, stubFetch, type } from "./helpers";

const base = f.meBody.tenants[0] as MeTenant;

describe("projects home", () => {
  it("finds projects by name in any case and by address", () => {
    const list = [
      base,
      { ...base, id: "b", name: "Lumen Pay", slug: "lumen-pay" },
      { ...base, id: "c", name: "Anchor Bridge", slug: "anchor" },
    ];
    expect(matchProjects(list, "").length).toBe(3);
    expect(matchProjects(list, "  LUMEN ").map((p) => p.id)).toEqual(["b"]);
    expect(matchProjects(list, "anchor").map((p) => p.id)).toEqual(["c"]);
    expect(matchProjects(list, "nothing")).toEqual([]);
  });

  it("names the one thing that waits, in amber only when the person must act", () => {
    expect(nextStep(base)).toBeNull();
    expect(nextStep({ ...base, status: "rejected" })).toEqual({
      text: "Change the answers and send them again.",
      act: true,
    });
    expect(nextStep({ ...base, status: "rejected", role: "member" })?.act).toBe(false);
    expect(nextStep({ ...base, terms_current: false })).toEqual({
      text: "Accept the new terms.",
      act: true,
    });
    expect(nextStep({ ...base, status: "pending" })).toEqual({
      text: "Operators review the project.",
      act: false,
    });
    expect(nextStep({ ...base, status: "suspended" })?.act).toBe(false);
  });
});

describe("enroll", () => {
  it("keeps the optional groups closed and opens the one with a refused field", async () => {
    stubFetch([]);
    const host = await render(<EnrollForm termsVersion={f.TERMS_VERSION} />);
    const groups = host.querySelectorAll("details");
    expect([...groups].map((d) => d.open)).toEqual([false, false]);
    await type(byLabel(host, "Short name"), "Not A Slug");
    await click(byRole(host, "button", "Enroll project"));
    expect(groups[0]?.open).toBe(true);
    expect(groups[1]?.open).toBe(false);
  });
});
