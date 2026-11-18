import { describe, expect, it, vi } from "vitest";
import { type Column, DataTable } from "@/components/data-table";
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

const table = (host: HTMLElement) => host.querySelector("table") as HTMLTableElement;

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
    // a column not marked for the phone stays in the table only
    expect(card.textContent).not.toContain("Germany");
  });
});
