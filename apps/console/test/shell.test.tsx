import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { areaOf, currentItem, pageTitle } from "@/components/nav/nav-items";
import { AppShell } from "@/pages/app-shell";
import { homeRedirect } from "@/pages/home/projects";
import * as f from "./fixtures/contract";
import { click, renderRoutes, waitFor } from "./helpers";

// answers by url, so the shell's parallel reads need no fixed order
function serve(me: unknown, alerts: unknown[] = []) {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      urls.push(input);
      const json = (status: number, body: unknown) =>
        new Response(JSON.stringify(body), {
          status,
          headers: { "Content-Type": "application/json; charset=utf-8" },
        });
      if (input === "/api/me") return json(200, me);
      if (input.startsWith(`/api/tenants/${f.TENANT_ID}`)) return json(200, f.tenantBody);
      if (input === "/api/operator/step-up") return json(200, f.steppedUpBody);
      if (input === "/api/operator/alerts")
        return json(200, {
          checked_at: "2026-11-13T12:00:05Z",
          watchdog_firing: true,
          sources: [
            { address: "10.0.1.4:9093", reachable: true, watchdog_firing: true, error: null },
          ],
          alerts,
        });
      if (input.startsWith("/api/operator/tenants"))
        return json(200, { items: [f.operatorSummaryBody], next_cursor: null });
      throw new Error(`unexpected request ${input}`);
    }),
  );
  return urls;
}

const page = { element: <p>page body</p> };
const routes = [
  {
    path: "/",
    element: <AppShell />,
    children: [
      { index: true, ...page },
      { path: "account", ...page },
      { path: "projects/:tenantId/*", ...page },
      { path: "operator/*", ...page },
    ],
  },
];

const critical = {
  fingerprint: "f-down",
  name: "ServiceDown",
  severity: "page",
  summary: "controlplane does not answer",
  description: null,
  node: "core-1",
  instance: null,
  service: "controlplane",
  starts_at: "2026-11-13T09:55:00Z",
  state: "active",
  inhibited_by: [],
  runbook: null,
  labels: {},
};

function current(host: HTMLElement): string[] {
  return [...host.querySelectorAll('aside a[aria-current="page"]')].map((a) => a.textContent ?? "");
}

describe("navigation model", () => {
  const id = f.TENANT_ID;
  it("finds the current page on every route, sub-pages included", () => {
    expect(currentItem(`/projects/${id}`)?.key).toBe("overview");
    expect(currentItem(`/projects/${id}/sites/s1`)?.key).toBe("sites");
    expect(currentItem(`/projects/${id}/sites/s1/settings`)?.key).toBe("sites");
    expect(currentItem(`/projects/${id}/api`)?.key).toBe("api");
    expect(currentItem(`/projects/${id}/ai`)?.key).toBe("ai");
    expect(currentItem(`/projects/${id}/connect-api`)?.key).toBe("connect-api");
    expect(currentItem(`/projects/${id}/scf`)?.key).toBe("scf");
    expect(currentItem("/operator")?.key).toBe("review");
    expect(currentItem("/operator/projects/abc")?.key).toBe("projects");
    expect(currentItem("/operator/keys")?.key).toBe("keys");
    expect(currentItem("/account")?.key).toBe("account");
    expect(currentItem(`/projects/${id}/unknown`)).toBeNull();
  });

  it("starts an operator without projects on the review page", () => {
    expect(homeRedirect(f.operatorMeBody as never)).toBe("/operator");
    expect(homeRedirect(f.meBody as never)).toBeNull();
  });

  it("names the page for the phone top bar", () => {
    expect(pageTitle(`/projects/${id}/web`)).toBe("Web");
    expect(pageTitle("/projects/new")).toBe("Enroll a project");
    expect(pageTitle("/operator/audit")).toBe("Audit log");
    expect(areaOf("/projects/new").kind).toBe("home");
  });
});

describe("console shell", () => {
  it("groups the project pages and marks Sites on a site's page", async () => {
    serve(f.meBody);
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}/sites/${f.SITE_ID}`);
    await waitFor(() => expect(host.textContent).toContain("page body"));
    const nav = host.querySelector('aside nav[aria-label="Project"]') as HTMLElement;
    for (const group of ["Data", "Funding", "Setup", "Team"])
      expect(nav.textContent).toContain(group);
    expect(current(host)).toEqual(["Sites"]);
    expect(host.querySelector('[data-testid="page-title"]')?.textContent).toBe("Stellar DEX");
    // no operator group for a member, and no second-level tab bar
    expect(host.querySelector('nav[aria-label="Operator"]')).toBeNull();
    expect(host.querySelectorAll("nav").length).toBeGreaterThan(0);
    expect(host.textContent).toContain("Account");
  });

  it("marks the account menu, the one way to Account, on the account page", async () => {
    serve(f.meBody);
    const host = await renderRoutes(routes, "/account");
    await waitFor(() =>
      expect(host.querySelector('aside button[aria-current="page"]')?.textContent).toContain(
        "Account menu",
      ),
    );
    expect(current(host)).toEqual([]);
  });

  it("shows the alert note on operator pages while a paging alert fires", async () => {
    serve(f.operatorMeBody, [critical]);
    const host = await renderRoutes(routes, "/operator/projects");
    await waitFor(() => expect(host.textContent).toContain("1 critical alert is firing."));
    const open = [...host.querySelectorAll("a")].find((a) => a.textContent === "Open alerts");
    expect(open?.getAttribute("href")).toBe("/operator/alerts");
    expect(current(host)).toEqual(["All projects"]);
    // the badges on Review and Alerts
    const alerts = [...host.querySelectorAll("aside a")].find((a) =>
      a.textContent?.startsWith("Alerts"),
    );
    await waitFor(() => expect(alerts?.textContent).toBe("Alerts11 critical alert firing"));
    const review = [...host.querySelectorAll("aside a")].find((a) =>
      a.textContent?.startsWith("Review"),
    );
    await waitFor(() => expect(review?.textContent).toBe("Review11 project waits for review"));
  });

  it("reads no operator data outside the operator area", async () => {
    const urls = serve(f.operatorMeBody, [critical]);
    const host = await renderRoutes(routes, "/account");
    await waitFor(() => expect(host.textContent).toContain("page body"));
    expect(host.querySelector('nav[aria-label="Operator"]')).not.toBeNull();
    expect(urls.filter((u) => u.startsWith("/api/operator"))).toEqual([]);
    expect(host.textContent).not.toContain("critical alert");
  });

  it("opens the command palette with ctrl k and goes to a page", async () => {
    serve(f.meBody);
    const host = await renderRoutes(routes, "/");
    await waitFor(() => expect(host.textContent).toContain("page body"));
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "k", ctrlKey: true }));
    });
    const dialog = document.querySelector('dialog[aria-label="Go to a page or a project"]');
    expect(dialog?.hasAttribute("open")).toBe(true);
    const input = dialog?.querySelector("input") as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    await act(async () => {
      setter?.call(input, "members");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const options = [...(dialog?.querySelectorAll('[role="option"]') ?? [])];
    // the project is said once, in the group header
    expect(options.map((o) => o.textContent)).toEqual(["Members"]);
    expect(dialog?.textContent).toContain("Pages of Stellar DEX");
    await act(async () => {
      input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });
    await waitFor(() => expect(current(host)).toEqual(["Members"]));
    expect(dialog?.hasAttribute("open")).toBe(false);
  });

  it("finds a project by name and says when nothing matches", async () => {
    serve(f.meBody);
    const host = await renderRoutes(routes, "/");
    await waitFor(() => expect(host.textContent).toContain("page body"));
    const search = [...host.querySelectorAll("button")].find(
      (b) => b.getAttribute("aria-label") === "Search pages and projects",
    );
    if (!search) throw new Error("no search button");
    await click(search);
    const dialog = document.querySelector('dialog[aria-label="Go to a page or a project"]');
    const input = dialog?.querySelector("input") as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    await act(async () => {
      setter?.call(input, "dex");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(dialog?.textContent).toContain("My projects");
    expect(dialog?.textContent).toContain("Stellar DEX");
    await act(async () => {
      setter?.call(input, "zzz");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(dialog?.textContent).toContain("No page or project matches. Try another name.");
  });
});
