import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FilterBar } from "@/components/filter-bar";
import { byLabel, byRole, click, render, type } from "./helpers";

function Harness({ onSearch }: { onSearch: (v: string) => void }) {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  return (
    <FilterBar
      search={q}
      onSearch={(v) => {
        setQ(v);
        onSearch(v);
      }}
      filters={[
        {
          key: "status",
          label: "Status",
          control: (
            <label>
              Status
              <select value={status} onChange={(e) => setStatus(e.target.value)}>
                <option value="">Any</option>
                <option value="active">Active</option>
              </select>
            </label>
          ),
          active: status ? { label: "Active", onClear: () => setStatus("") } : null,
        },
      ]}
      onClearAll={() => {
        setQ("");
        setStatus("");
      }}
    />
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("filter bar", () => {
  it("filters as the person types, shows chips and clears them", async () => {
    const onSearch = vi.fn();
    const host = await render(<Harness onSearch={onSearch} />);
    expect(host.textContent).not.toContain("Clear filters");
    await type(byLabel(host, "Search"), "dex");
    expect(onSearch).toHaveBeenLastCalledWith("dex");
    expect(byRole(host, "button", "Clear filters")).toBeTruthy();

    const select = host.querySelector("select") as HTMLSelectElement;
    const { act } = await import("react");
    await act(async () => {
      select.value = "active";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const chips = host.querySelector('ul[aria-label="Active filters"]') as HTMLElement;
    expect(chips.textContent).toContain("Status:Active");
    await click(chips.querySelector('[aria-label="Remove filter Status"]') as HTMLElement);
    expect(host.querySelector('ul[aria-label="Active filters"]')).toBeNull();

    await click(byRole(host, "button", "Clear filters"));
    expect((byLabel(host, "Search") as HTMLInputElement).value).toBe("");
    expect(host.textContent).not.toContain("Clear filters");
  });

  it("moves the filters into a sheet on a phone, with the active count", async () => {
    vi.stubGlobal("matchMedia", (q: string) => ({
      matches: true,
      media: q,
      addEventListener() {},
      removeEventListener() {},
    }));
    const host = await render(
      <FilterBar
        filters={[
          {
            key: "status",
            label: "Status",
            control: <span>status control</span>,
            active: { label: "Active", onClear: () => {} },
          },
        ]}
      />,
    );
    const button = byRole(host, "button", "Filters1 active");
    expect(host.querySelector("dialog")?.hasAttribute("open")).toBe(false);
    await click(button);
    expect(host.querySelector("dialog")?.hasAttribute("open")).toBe(true);
    expect(host.querySelector("dialog")?.textContent).toContain("status control");
  });

  it("keeps every filter behind the Filters button on a wide screen when collapsed", async () => {
    const host = await render(
      <FilterBar
        collapsed
        filters={[{ key: "grade", label: "Grade", control: <span>grade control</span> }]}
        actions={<button type="button">Columns</button>}
      />,
    );
    expect(byRole(host, "button", "Filters")).toBeTruthy();
    expect(byRole(host, "button", "Columns")).toBeTruthy();
    expect(host.querySelector("dialog")?.textContent).toContain("grade control");
    expect(host.querySelector("dialog")?.hasAttribute("open")).toBe(false);
  });
});
