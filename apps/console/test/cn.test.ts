import { cn } from "@veristar/brand/cn";
import { describe, expect, it } from "vitest";

// the brand's type steps, radii and shadows must merge in their own groups
describe("cn with the brand scale", () => {
  it("keeps a type step next to a text colour", () => {
    expect(cn("font-mono text-metric", "text-fg")).toBe("font-mono text-metric text-fg");
  });
  it("lets a later type step replace an earlier one", () => {
    expect(cn("text-body", "text-small")).toBe("text-small");
  });
  it("lets a later colour replace an earlier one", () => {
    expect(cn("text-fg-2", "text-critical")).toBe("text-critical");
  });
  it("merges radius and shadow tokens", () => {
    expect(cn("rounded-md shadow-overlay", "rounded-lg shadow-dialog")).toBe("rounded-lg shadow-dialog");
  });
});
