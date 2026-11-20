import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/api/client";
import { BreakdownList, type TimeBucket, TimeChart, type TimeSeries } from "@/components/charts";
import { partShare } from "@/components/charts/breakdown-list";
import { byRole, click, render, waitFor } from "./helpers";

const buckets: TimeBucket[] = [
  { at: "2026-11-16T00:00:00Z", label: "Nov 16, 2026", tick: "Nov 16" },
  { at: "2026-11-17T00:00:00Z", label: "Nov 17, 2026", tick: "Nov 17" },
  { at: "2026-11-18T00:00:00Z", label: "Nov 18, 2026", tick: "Nov 18", partial: true },
];
const series: TimeSeries[] = [
  { key: "views", label: "Page views", tone: "series-1", values: [1200, 34567, 10] },
];

async function press(el: Element, key: string) {
  await act(async () => {
    el.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
  });
}

describe("time chart, sparse data", () => {
  it("shows the first data at its time instead of an axis with one bar", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="data"
        buckets={buckets}
        series={[{ key: "views", label: "Page views", tone: "series-1", values: [0, 0, 79] }]}
      />,
    );
    const first = host.querySelector("[data-first-data]");
    expect(first?.getAttribute("data-first-data")).toBe("2026-11-18T00:00:00Z");
    expect(first?.textContent).toContain("Nov 18, 2026");
    expect(first?.textContent).toContain("79");
    expect(host.querySelector("[role=group]")).toBeNull();
  });

  it("starts a line at its first value, not with a rise from zero", async () => {
    const host = await render(
      <TimeChart
        title="Visitors"
        kind="line"
        state="data"
        buckets={[...buckets, { at: "2026-11-19T00:00:00Z", label: "Nov 19", tick: "Nov 19" }]}
        series={[{ key: "v", label: "Visitors", tone: "series-1", values: [0, 0, 4, 6] }]}
      />,
    );
    const line = host.querySelector("polyline[data-series=v]");
    expect(line?.getAttribute("points")?.split(" ")).toHaveLength(2);
  });
});

describe("time chart", () => {
  it("shows a loading state and keeps the title", async () => {
    const host = await render(
      <TimeChart title="Page views" state="loading" buckets={[]} series={[]} />,
    );
    expect(host.querySelector("h2")?.textContent).toBe("Page views");
    expect(host.querySelector("[aria-busy=true]")?.textContent).toContain("Loading the chart");
  });

  it("shows an error with Try again and Copy details", async () => {
    const retry = vi.fn();
    const err = new ApiError({ status: 500, code: "internal", message: "boom", requestId: "r1" });
    const host = await render(
      <TimeChart
        title="Page views"
        state="error"
        error={err}
        onRetry={retry}
        buckets={[]}
        series={[]}
      />,
    );
    expect(host.querySelector("[role=alert]")?.textContent).toContain("The chart did not load");
    expect(host.textContent).not.toContain("r1");
    await click(byRole(host, "button", "Try again"));
    expect(retry).toHaveBeenCalledOnce();
    byRole(host, "button", "Copy details");
  });

  it("shows an empty state without a chart frame", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="empty"
        buckets={[]}
        series={[]}
        emptyTitle="No page views in this range"
      />,
    );
    expect(host.textContent).toContain("No page views in this range");
    expect(host.querySelector("svg [data-partial], [role=group]")).toBeNull();
  });

  it("says so when no period is complete", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="data"
        buckets={[buckets[2] as TimeBucket]}
        series={[{ ...(series[0] as TimeSeries), values: [10] }]}
        noCompleteTitle="No complete day yet"
      />,
    );
    expect(host.textContent).toContain("No complete day yet");
    expect(host.querySelector("[role=group]")).toBeNull();
  });

  it("names partial periods in the legend only when there is one", async () => {
    const host = await render(
      <TimeChart title="Page views" state="data" buckets={buckets} series={series} />,
    );
    expect(host.querySelector("[data-legend=partial]")?.textContent).toBe("Still running");
    expect(host.querySelectorAll("[data-partial]")).toHaveLength(1);
    const whole = await render(
      <TimeChart
        title="Visits"
        state="data"
        buckets={buckets.slice(0, 2)}
        series={[{ ...(series[0] as TimeSeries), values: [1, 2] }]}
      />,
    );
    expect(whole.querySelector("[data-legend=partial]")).toBeNull();
  });

  it("leaves out the legend when the page shows one for a group of charts", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="data"
        buckets={buckets}
        series={series}
        legend={false}
      />,
    );
    expect(host.querySelector("ul[aria-label=Legend]")).toBeNull();
    // the hatching still marks the partial period
    expect(host.querySelectorAll("[data-partial]")).toHaveLength(1);
  });

  it("toggles the previous period", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="data"
        buckets={buckets}
        series={series}
        previous={[1000, 30000, 20]}
      />,
    );
    const toggle = byRole(host, "button", "Previous period");
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    expect(host.querySelector("[data-previous]")).not.toBeNull();
    await click(toggle);
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    expect(host.querySelector("[data-previous]")).toBeNull();
  });

  it("lists every bucket with exact values in the table view", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="data"
        buckets={buckets}
        series={series}
        previous={[1000, null, 20]}
      />,
    );
    const toggle = host.querySelector<HTMLButtonElement>('button[aria-label="Show as table"]');
    if (!toggle) throw new Error("no table toggle");
    await click(toggle);
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
    // the chart fades out before the table comes in
    await waitFor(() => expect(host.querySelector("table")).not.toBeNull());
    const head = [...host.querySelectorAll("thead th")].map((t) => t.textContent);
    expect(head).toEqual(["Time (UTC)", "Page views", "Previous period"]);
    const rows = [...host.querySelectorAll("tbody tr")].map((r) =>
      [...r.children].map((c) => c.textContent),
    );
    expect(rows).toEqual([
      ["Nov 16, 2026", "1,200", "1,000"],
      ["Nov 17, 2026", "34,567", "–"],
      ["Nov 18, 2026 (partial)", "10", "20"],
    ]);
  });

  it("reads each bucket with the arrow keys", async () => {
    const host = await render(
      <TimeChart
        title="Page views"
        state="data"
        buckets={buckets}
        series={series}
        previous={[1000, 30000, 20]}
      />,
    );
    const group = host.querySelector("[role=group]");
    if (!group) throw new Error("no plot");
    const live = () => host.querySelector("[aria-live=polite]")?.textContent;
    await press(group, "ArrowRight");
    expect(live()).toBe("Nov 18, 2026, partial, Page views 10, Previous period 20, change -50%");
    await press(group, "Home");
    expect(live()).toBe("Nov 16, 2026, Page views 1,200, Previous period 1,000, change +20%");
    await press(group, "ArrowRight");
    expect(live()).toContain("Nov 17, 2026, Page views 34,567");
    await press(group, "Escape");
    expect(live()).toBe("");
  });

  it("draws lines with gaps for missing values", async () => {
    const host = await render(
      <TimeChart
        title="p95"
        kind="line"
        state="data"
        buckets={[...buckets.slice(0, 2), { at: "2026-11-18", label: "Nov 18", tick: "18" }]}
        series={[{ key: "p95", label: "p95", tone: "series-1", values: [0.2, null, 0.4] }]}
        ceiling={{ value: 10, label: ">10 s above" }}
        marker={{ at: "2026-11-17", label: "Award date" }}
      />,
    );
    // two runs of one point each, so two dots and no line across the gap
    expect(host.querySelectorAll('[data-series="p95"]')).toHaveLength(2);
    expect(host.querySelector("polyline[data-series]")).toBeNull();
    expect(host.querySelector("[data-ceiling]")?.textContent).toBe(">10 s above");
    expect(host.querySelector("[data-marker]")?.textContent).toBe("Award date");
  });

  it("draws one marker per compared project and bands the running period of a line", async () => {
    const host = await render(
      <TimeChart
        title="Visitors"
        kind="line"
        state="data"
        buckets={buckets}
        series={[
          { key: "a", label: "Lumen Pay", tone: "series-1", values: [3, 4, 1] },
          { key: "b", label: "Orbit Labs", tone: "series-2", values: [1, 2, 0] },
        ]}
        markers={[
          { at: "2026-11-16", label: "Lumen Pay awarded" },
          { at: "2026-11-17", label: "Orbit Labs awarded" },
        ]}
      />,
    );
    const marks = [...host.querySelectorAll("[data-marker]")].map((m) => m.textContent);
    expect(marks).toEqual(["Lumen Pay awarded", "Orbit Labs awarded"]);
    expect(host.querySelectorAll("rect[data-partial]")).toHaveLength(1);
    expect(host.querySelector("[data-legend=partial]")?.textContent).toBe("Still running");
  });
});

describe("breakdown list", () => {
  const items = Array.from({ length: 12 }, (_, i) => ({
    key: `k${i}`,
    label: `Page ${i}`,
    text: `Page ${i}`,
    value: 1200 - i * 100,
  }));

  it("shows shares of the total and exact values, then more on request", async () => {
    const host = await render(
      <BreakdownList label="Pages" items={items} total={2400} valueLabel="Visitors" />,
    );
    const rows = host.querySelectorAll('ul[aria-label="Pages"] li');
    expect(rows).toHaveLength(10);
    expect(rows[0]?.textContent).toContain("1,200");
    expect(rows[0]?.textContent).toContain("50%");
    await click(byRole(host, "button", "Show 2 more"));
    expect(host.querySelectorAll('ul[aria-label="Pages"] li')).toHaveLength(12);
  });

  it("selects a key", async () => {
    const pick = vi.fn();
    const host = await render(
      <BreakdownList label="Pages" items={items.slice(0, 2)} onSelect={pick} selectedKey="k1" />,
    );
    const buttons = host.querySelectorAll("li button");
    expect(buttons[1]?.getAttribute("aria-pressed")).toBe("true");
    await click(buttons[0] as HTMLElement);
    expect(pick).toHaveBeenCalledWith("k0");
  });

  it("has empty, loading and error states", async () => {
    const empty = await render(
      <BreakdownList label="Countries" items={[]} emptyTitle="No countries in this range" />,
    );
    expect(empty.textContent).toContain("No countries in this range");
    const loading = await render(<BreakdownList label="Countries" items={[]} state="loading" />);
    expect(loading.querySelector("[aria-busy=true]")?.textContent).toContain("Loading countries");
    const retry = vi.fn();
    const failed = await render(
      <BreakdownList
        label="Countries"
        items={[]}
        state="error"
        error={new TypeError("x")}
        onRetry={retry}
      />,
    );
    await click(byRole(failed, "button", "Try again"));
    expect(retry).toHaveBeenCalledOnce();
  });
});

describe("breakdown shares", () => {
  it("never shows a part smaller than the whole as 100%", () => {
    expect(partShare(998, 1000)).toBe("99.8%");
    expect(partShare(2, 1000)).toBe("0.2%");
    expect(partShare(1000, 1000)).toBe("100%");
    expect(partShare(500, 1000)).toBe("50%");
  });
});
