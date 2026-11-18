import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import {
  type CustomRange,
  DateField,
  isIsoDay,
  parseUtcDayTime,
  RangeControl,
  validateRange,
} from "@/components/range-control";
import { byLabel, byRole, click, render, type } from "./helpers";

const PRESETS = [
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
] as const;

function Harness({ onCustom }: { onCustom: (r: CustomRange) => void }) {
  const [value, setValue] = useState<"7" | "30" | "custom">("30");
  const [custom, setCustom] = useState<CustomRange | null>(null);
  return (
    <RangeControl
      value={value}
      presets={PRESETS}
      onPreset={setValue}
      custom={custom}
      onCustom={(r) => {
        setCustom(r);
        setValue("custom");
        onCustom(r);
      }}
      range={custom ?? { from: "2026-10-20", to: "2026-11-18" }}
      maxDays={90}
    />
  );
}

describe("date field", () => {
  it("takes 2026-11-18 and refuses other shapes", async () => {
    expect(isIsoDay("2026-11-18")).toBe(true);
    expect(isIsoDay("11/18/2026")).toBe(false);
    expect(isIsoDay("2026-02-30")).toBe(false);
    expect(parseUtcDayTime("2026-11-18 14:00")).toBe("2026-11-18T14:00:00Z");
    expect(parseUtcDayTime("2026-11-18 24:00")).toBeNull();

    function One() {
      const [v, setV] = useState("");
      return <DateField label="Award date" value={v} onChange={setV} />;
    }
    const host = await render(<One />);
    const input = byLabel(host, "Award date") as HTMLInputElement;
    expect(input.type).toBe("text");
    await type(input, "11/18/2026");
    const { act } = await import("react");
    await act(async () => {
      input.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
    });
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(host.textContent).toContain("Enter a day as 2026-11-18.");
    await type(input, "2026-11-18");
    expect(input.hasAttribute("aria-invalid")).toBe(false);
    expect(host.textContent).toContain("UTC day, as 2026-11-18");
  });
});

describe("range control", () => {
  it("changes presets and says the range with UTC", async () => {
    const host = await render(<Harness onCustom={() => {}} />);
    const group = host.querySelector('[role="radiogroup"][aria-label="Range"]') as HTMLElement;
    expect(group.querySelector('[aria-checked="true"]')?.textContent).toBe("30 days");
    await click(byRole(group, "radio", "7 days"));
    expect(group.querySelector('[aria-checked="true"]')?.textContent).toBe("7 days");
    expect(host.textContent).toContain("Oct 20 to Nov 18, 2026, UTC");
  });

  it("checks and applies a custom range", async () => {
    const onCustom = vi.fn();
    const host = await render(<Harness onCustom={onCustom} />);
    await click(byRole(host, "radio", "Custom"));
    const form = host.querySelector('form[aria-label="Custom range"]') as HTMLFormElement;
    await type(byLabel(form, "From (UTC day)"), "2026-11-10");
    await type(byLabel(form, "To (UTC day)"), "2026-11-01");
    await click(byRole(form, "button", "Apply range"));
    expect(form.textContent).toContain("End on or after the start.");
    expect(onCustom).not.toHaveBeenCalled();

    await type(byLabel(form, "To (UTC day)"), "2026-11-12");
    await click(byRole(form, "button", "Apply range"));
    expect(onCustom).toHaveBeenCalledWith({ from: "2026-11-10", to: "2026-11-12" });
    expect(host.querySelector('form[aria-label="Custom range"]')).toBeNull();
    expect(host.textContent).toContain("Nov 10 to Nov 12, 2026, UTC");
  });

  it("validates limits", () => {
    expect(validateRange({ from: "11/1/2026", to: "2026-11-02" })).toEqual({
      from: "Enter a day as 2026-11-18.",
    });
    expect(validateRange({ from: "2026-01-01", to: "2026-12-31" }, { maxDays: 90 }).to).toBe(
      "A range covers at most 90 days.",
    );
    expect(
      validateRange({ from: "2026-11-01", to: "2026-11-30" }, { maxDay: "2026-11-18" }).to,
    ).toBe("End on or before 2026-11-18.");
    expect(
      validateRange({ from: "2026-11-18 10:00", to: "2026-11-18 09:00" }, { time: true }).to,
    ).toBe("End after the start.");
  });
});
