import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type Column, DataTable, useRowPages } from "@/components/data-table";
import { byRole, click, render, waitFor } from "./helpers";

type Row = { id: string; name: string; visits: number; country: string };

const rows: Row[] = [
  { id: "a", name: "Stellar DEX", visits: 1200, country: "Germany" },
  { id: "b", name: "Anchor", visits: 30, country: "France" },
];

const columns: Column<Row>[] = [
  { key: "name", header: "Name", cell: (r) => r.name, sortKey: "name", primary: true },
  {
    key: "visits",
    header: "Visits",
    cell: (r) => r.visits.toLocaleString("en"),
    sortKey: "visits",
    align: "right",
    numeric: true,
    mobile: true,
  },
  { key: "country", header: "Country", cell: (r) => r.country, truncate: (r) => r.country },
];

const table = (host: HTMLElement) => host.querySelector("section table") as HTMLTableElement;

afterEach(() => {
  vi.restoreAllMocks();
});

// jsdom has no layout: a table wider than its frame is a table whose width
// is more than the frame's
function layout(tableWidth: number, frameWidth: number) {
  vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockImplementation(function (
    this: HTMLElement,
  ) {
    return this.tagName === "TABLE" ? tableWidth : 0;
  });
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(function (
    this: HTMLElement,
  ) {
    return this.tagName === "SECTION" ? frameWidth : 0;
  });
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(function (
    this: HTMLElement,
  ) {
    return this.tagName === "SECTION" ? Math.max(tableWidth, frameWidth) : 0;
  });
}

describe("data table", () => {
  it("sorts by its headers with aria-sort", async () => {
    const onSort = vi.fn();
    const host = await render(
      <DataTable
        label="Projects table"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        sort={{ key: "visits", order: "desc" }}
        onSort={onSort}
      />,
    );
    const ths = [...table(host).querySelectorAll("th")];
    expect(ths[0]?.getAttribute("aria-sort")).toBe("none");
    expect(ths[1]?.getAttribute("aria-sort")).toBe("descending");
    expect(ths[2]?.hasAttribute("aria-sort")).toBe(false);
    await click(ths[0]?.querySelector("button") as HTMLElement);
    expect(onSort).toHaveBeenCalledWith("name");
    expect(host.querySelector("section")?.getAttribute("aria-label")).toBe("Projects table");
  });

  it("carries a column definition on its header for hover and keyboard", async () => {
    const defined: Column<Row>[] = [
      { ...columns[0], definition: undefined } as Column<Row>,
      { ...(columns[1] as Column<Row>), definition: "Visits in the range." },
      { ...(columns[2] as Column<Row>), definition: "Where most visits came from." },
    ];
    const host = await render(
      <DataTable
        label="Projects table"
        columns={defined}
        rows={rows}
        getRowKey={(r) => r.id}
        sort={{ key: "visits", order: "desc" }}
        onSort={() => {}}
      />,
    );
    const ths = [...table(host).querySelectorAll("th")];
    expect(ths[1]?.querySelector("button")?.getAttribute("aria-description")).toBe(
      "Visits in the range.",
    );
    const plain = ths[2]?.querySelector("[tabindex='0']");
    expect(plain?.textContent).toContain("Where most visits came from.");
  });

  it("links each row and passes data attributes through", async () => {
    const host = await render(
      <DataTable
        label="Projects table"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        rowHref={(r) => `/operator/projects/${r.id}`}
        rowProps={(r) => ({ "data-tenant": r.id })}
      />,
    );
    const tr = table(host).querySelector('tr[data-tenant="a"]');
    expect(tr?.querySelector("a")?.getAttribute("href")).toBe("/operator/projects/a");
    expect(tr?.querySelectorAll("a")).toHaveLength(1);
    expect(tr?.textContent).toContain("1,200");
  });

  it("shows loading, empty and error rows", async () => {
    const loading = await render(
      <DataTable label="T" columns={columns} rows={[]} getRowKey={(r) => r.id} state="loading" />,
    );
    expect(table(loading).querySelector("tbody")?.getAttribute("aria-busy")).toBe("true");
    expect(table(loading).querySelectorAll("tbody tr")).toHaveLength(5);

    const empty = await render(
      <DataTable
        label="T"
        columns={columns}
        rows={[]}
        getRowKey={(r) => r.id}
        empty={{ title: "No project matches", text: "Change the filters." }}
      />,
    );
    expect(table(empty).textContent).toContain("No project matches");
    expect(table(empty).querySelector("td")?.getAttribute("colspan")).toBe("3");

    const onRetry = vi.fn();
    const failed = await render(
      <DataTable
        label="T"
        columns={columns}
        rows={[]}
        getRowKey={(r) => r.id}
        state="error"
        error={new Error("boom")}
        onRetry={onRetry}
      />,
    );
    const cell = table(failed).querySelector("td") as HTMLElement;
    expect(cell.querySelector('[role="alert"]')).not.toBeNull();
    await click(byRole(cell, "button", "Try again"));
    expect(onRetry).toHaveBeenCalled();
    expect(byRole(cell, "button", "Copy details")).toBeTruthy();
  });

  it("loads more and pages", async () => {
    const onMore = vi.fn();
    const more = await render(
      <DataTable
        label="T"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        footer={{ kind: "more", hasMore: true, loading: false, onMore }}
      />,
    );
    await click(byRole(more, "button", "Load more"));
    expect(onMore).toHaveBeenCalled();

    const onPage = vi.fn();
    const pager = await render(
      <DataTable
        label="T"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        footer={{ kind: "pager", page: 1, pageSize: 50, total: 134, onPage }}
      />,
    );
    expect(pager.textContent).toContain("51 to 100 of 134");
    await click(pager.querySelector('[aria-label="Next page"]') as HTMLElement);
    expect(onPage).toHaveBeenCalledWith(2);
  });

  it("selects rows one by one and all on the page", async () => {
    const onSelect = vi.fn();
    const host = await render(
      <DataTable
        label="T"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        rowLabel={(r) => r.name}
        selection={{ selected: new Set(["b"]), onSelect }}
      />,
    );
    const box = table(host).querySelector('[aria-label="Select Stellar DEX"]') as HTMLElement;
    await click(box);
    expect([...(onSelect.mock.calls[0]?.[0] ?? [])].sort()).toEqual(["a", "b"]);
    await click(table(host).querySelector('[aria-label="Select all on this page"]') as HTMLElement);
    expect([...(onSelect.mock.calls[1]?.[0] ?? [])].sort()).toEqual(["a", "b"]);
    expect(table(host).querySelector('tr[data-state="selected"]')?.textContent).toContain("Anchor");
  });

  it("renders phone cards with the name line and the mobile columns", async () => {
    const host = await render(
      <DataTable
        label="Projects table"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        rowHref={(r) => `/p/${r.id}`}
      />,
    );
    await waitFor(() =>
      expect(host.querySelector("ul[aria-label='Projects table']")).not.toBeNull(),
    );
    const card = host.querySelector("ul[aria-label='Projects table'] li") as HTMLElement;
    expect(card.querySelector("a")?.getAttribute("href")).toBe("/p/a");
    expect(card.querySelector("dt")?.textContent).toBe("Visits");
    expect(card.textContent).toContain("1,200");
    // a column not marked for the phone is behind the card's Details, never
    // dropped
    const details = card.querySelector("details") as HTMLDetailsElement;
    expect(details.open).toBe(false);
    expect(details.querySelector("summary")?.textContent).toBe("Details");
    expect(details.textContent).toContain("Germany");
  });

  it("scrolls with the page and sticks its header when the columns fit", async () => {
    layout(800, 800);
    const host = await render(
      <DataTable label="Projects table" columns={columns} rows={rows} getRowKey={(r) => r.id} />,
    );
    const frame = host.querySelector("section") as HTMLElement;
    // no scroll region of its own: no height cap, no overflow, no tab stop
    expect(frame.className).not.toMatch(/max-h|overflow/);
    expect(frame.hasAttribute("tabindex")).toBe(false);
    expect(frame.hasAttribute("data-wide")).toBe(false);
    const th = table(host).querySelector("th") as HTMLElement;
    expect(th.className).toContain("sticky");
    // under the shell header below lg, at the window top from lg
    expect(th.className).toContain("top-(--vs-header)");
    expect(th.className).toContain("lg:top-0");
    expect(host.querySelector("[data-sticky-head]")).toBeNull();
    expect(host.querySelector("[data-fade]")).toBeNull();
  });

  it("scrolls sideways in its frame with edge fades when the columns do not fit", async () => {
    layout(1200, 800);
    const onSort = vi.fn();
    const host = await render(
      <DataTable
        label="Projects table"
        columns={columns}
        rows={rows}
        getRowKey={(r) => r.id}
        sort={{ key: "visits", order: "desc" }}
        onSort={onSort}
      />,
    );
    const frame = host.querySelector("section") as HTMLElement;
    expect(frame.hasAttribute("data-wide")).toBe(true);
    expect(frame.className).toContain("overflow-x-auto");
    expect(frame.className).not.toMatch(/max-h|overflow-y|overflow-auto/);
    expect(frame.getAttribute("tabindex")).toBe("0");
    // the real header does not stick inside the sideways scroll
    expect(table(host).querySelector("th")?.className).not.toContain("sticky");

    const start = host.querySelector('[data-fade="start"]') as HTMLElement;
    const end = host.querySelector('[data-fade="end"]') as HTMLElement;
    expect(start.className).not.toContain("opacity-100");
    expect(end.className).toContain("opacity-100");
    Object.defineProperty(frame, "scrollLeft", { configurable: true, value: 400 });
    await act(async () => {
      frame.dispatchEvent(new Event("scroll"));
    });
    expect(start.className).toContain("opacity-100");
    expect(end.className).not.toContain("opacity-100");

    // the copy of the header sticks to the window, hidden until the real
    // header leaves the view
    const copy = host.querySelector("[data-sticky-head]") as HTMLElement;
    expect(copy.getAttribute("aria-hidden")).toBe("true");
    expect(copy.className).toContain("sticky");
    expect(copy.className).toContain("invisible");
    const thead = table(host).querySelector("thead") as HTMLElement;
    vi.spyOn(thead, "getBoundingClientRect").mockReturnValue({
      top: -60,
      height: 36,
    } as DOMRect);
    vi.spyOn(table(host), "getBoundingClientRect").mockReturnValue({ bottom: 400 } as DOMRect);
    await act(async () => {
      window.dispatchEvent(new Event("scroll"));
    });
    expect(copy.className).not.toContain("invisible");
    // it follows the sideways scroll and sorts on click, out of the tab order
    expect((copy.querySelector("table") as HTMLElement).style.translate).toBe("-400px 0");
    const copied = copy.querySelector("button") as HTMLButtonElement;
    expect(copied.tabIndex).toBe(-1);
    await click(copied);
    expect(onSort).toHaveBeenCalledWith("name");

    // once the last row passes the top, the copy hides again
    vi.spyOn(table(host), "getBoundingClientRect").mockReturnValue({ bottom: 20 } as DOMRect);
    await act(async () => {
      window.dispatchEvent(new Event("scroll"));
    });
    expect(copy.className).toContain("invisible");
  });

  it("wraps a short value on a phone card and cuts only a column that truncates", async () => {
    const phone: Column<Row>[] = [
      columns[0] as Column<Row>,
      { key: "type", header: "Type", cell: () => "Machine API key", mobile: true },
      { ...(columns[2] as Column<Row>), mobile: true },
    ];
    const host = await render(
      <DataTable label="Keys" columns={phone} rows={rows} getRowKey={(r) => r.id} />,
    );
    const card = host.querySelector("ul[aria-label='Keys'] li") as HTMLElement;
    const [type, country] = [...card.querySelectorAll("dd")] as HTMLElement[];
    expect(type?.textContent).toBe("Machine API key");
    expect(type?.className).toContain("break-words");
    expect(type?.className).not.toContain("truncate");
    expect(type?.querySelector(".truncate")).toBeNull();
    expect(country?.querySelector(".truncate")?.textContent).toBe("Germany");
  });

  it("pages a list it holds in full", async () => {
    const many: Row[] = Array.from({ length: 25 }, (_, i) => ({
      id: `r${i}`,
      name: `Row ${i}`,
      visits: i,
      country: "Germany",
    }));
    function Paged() {
      const p = useRowPages(many, 10);
      return (
        <DataTable
          label="Paged"
          columns={columns}
          rows={p.rows}
          getRowKey={(r) => r.id}
          footer={p.footer}
        />
      );
    }
    const host = await render(<Paged />);
    await waitFor(() => expect(host.textContent).toContain("1 to 10 of 25"));
    expect(host.querySelectorAll("tbody tr")).toHaveLength(10);
    await click(host.querySelector("button[aria-label='Next page']") as HTMLElement);
    await waitFor(() => expect(host.textContent).toContain("11 to 20 of 25"));
    expect(host.textContent).toContain("Row 10");
  });
});
