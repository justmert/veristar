import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { reportApi } from "@/api/reports";
import { demoAllows, demoNav, OPERATOR_NAV, projectNav } from "@/components/nav/nav-items";
import { apiBase, enterDemo, isDemo, leaveDemo, useDemo } from "@/lib/demo";
import { errorMessage } from "@/lib/errors";
import { AppShell } from "@/pages/app-shell";
import { ProjectLayout, useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import { demoLoader } from "@/router";
import * as f from "./fixtures/contract";
import { render, renderRoutes, stubFetch, waitFor } from "./helpers";

// what /api/demo/me answers: the /api/me shape, the demo person, the sample
// projects with the viewer role and the demo marker
const OTHER_ID = "0c0ffee0-0000-4000-8000-000000000002";
const demoMe = {
  demo: true,
  user: {
    id: "ffffffff-ffff-ffff-ffff-ffffffffffff",
    github_id: null,
    github_login: null,
    stellar_address: null,
    passkey_address: null,
    display_name: "Demo",
    is_operator: true,
    operator_role: "operator",
  },
  tenants: [
    { ...f.meBody.tenants[0], role: "viewer" },
    { ...f.meBody.tenants[0], id: OTHER_ID, slug: "second", name: "Second", role: "viewer" },
  ],
  terms: f.meBody.terms,
  removed_tenants: [],
};

afterEach(() => {
  leaveDemo();
  queryClient.clear();
});

describe("demo detection", () => {
  it("is off until the tab enters the demo, and off again once it leaves", () => {
    expect(isDemo()).toBe(false);
    enterDemo();
    expect(isDemo()).toBe(true);
    expect(window.sessionStorage.getItem("sa-console.demo")).toBe("1");
    leaveDemo();
    expect(isDemo()).toBe(false);
  });

  it("draws again when the tab enters or leaves the demo", async () => {
    function Probe() {
      return <p data-demo={useDemo() ? "on" : "off"} />;
    }
    const host = await render(<Probe />);
    expect(host.querySelector("[data-demo]")?.getAttribute("data-demo")).toBe("off");
    await act(async () => enterDemo());
    expect(host.querySelector("[data-demo]")?.getAttribute("data-demo")).toBe("on");
    await act(async () => leaveDemo());
    expect(host.querySelector("[data-demo]")?.getAttribute("data-demo")).toBe("off");
  });
});

describe("api base", () => {
  it("sends every console route to /api/demo in a demo tab, and to /api outside it", () => {
    expect(apiBase("/me", false)).toBe("/api");
    expect(apiBase("/me", true)).toBe("/api/demo");
    expect(apiBase("/operator/web/projects?limit=50", true)).toBe("/api/demo");
  });

  it("keeps the public routes on /api, which the demo does not serve", () => {
    expect(apiBase("/terms", true)).toBe("/api");
    expect(apiBase("/public/reports/r1", true)).toBe("/api");
    // a path that only starts with the same letters is not the public route
    expect(apiBase("/termsheet", true)).toBe("/api/demo");
  });

  it("reads the demo person through /api/demo/me without the csrf header", async () => {
    enterDemo();
    const calls = stubFetch([{ status: 200, body: demoMe }]);
    const me = await api.me();
    expect(calls[0]?.url).toBe("/api/demo/me");
    expect(calls[0]?.headers.get("X-Requested-With")).toBeNull();
    expect(me.tenants.map((t) => t.role)).toEqual(["viewer", "viewer"]);
  });

  it("reads a shared report from /api/public in a demo tab too", async () => {
    enterDemo();
    const calls = stubFetch([{ status: 404, body: f.errorBody("not_found") }]);
    await reportApi.public("r1").catch(() => null);
    expect(calls[0]?.url).toBe("/api/public/reports/r1");
  });

  it("shows the server's message when the demo refuses a request", async () => {
    enterDemo();
    stubFetch([
      {
        status: 403,
        body: {
          error: {
            code: "demo_read_only",
            message: "The demo is read-only",
            request_id: "req-1",
          },
        },
      },
    ]);
    const err = await api
      .createSite(f.TENANT_ID, { name: "a", domain: "a.org", origins: [] })
      .catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("demo_read_only");
    expect(errorMessage(err)).toBe("The demo is read-only");
  });
});

describe("demo gating", () => {
  const id = f.TENANT_ID;
  const keysOf = (g: ReturnType<typeof demoNav>) => g.flatMap((x) => x.items.map((i) => i.key));

  it("lists the project pages that read only what the demo serves", () => {
    expect(keysOf(demoNav(projectNav(id), "project"))).toEqual([
      "overview",
      "web",
      "api",
      "onchain",
      "scf",
      "sites",
    ]);
    // a group left without a page is dropped, not drawn empty
    expect(demoNav(projectNav(id), "project").map((g) => g.key)).toEqual([
      "data",
      "funding",
      "setup",
    ]);
  });

  it("lists the operator pages that read only what the demo serves", () => {
    expect(keysOf(demoNav(OPERATOR_NAV, "operator"))).toEqual(["projects", "compare", "rules"]);
  });

  it("allows the readable pages and refuses the rest by address", () => {
    for (const p of [
      "/",
      `/projects/${id}`,
      `/projects/${id}/web`,
      `/projects/${id}/api`,
      `/projects/${id}/onchain`,
      `/projects/${id}/scf`,
      `/projects/${id}/sites`,
      `/projects/${id}/sites/s1`,
      `/projects/${id}/sites/s1/settings`,
      "/operator/projects",
      `/operator/projects/${id}`,
      "/operator/compare",
      "/operator/rules",
    ])
      expect(demoAllows(p), p).toBe(true);
    for (const p of [
      "/account",
      "/projects/new",
      `/projects/${id}/members`,
      `/projects/${id}/privacy`,
      `/projects/${id}/connect-api`,
      `/projects/${id}/ai`,
      `/projects/${id}/web/extra`,
      "/operator",
      "/operator/alerts",
      "/operator/audit",
      "/operator/operators",
      "/operator/settings",
      "/operator/keys",
    ])
      expect(demoAllows(p), p).toBe(false);
  });
});

describe("demo entry", () => {
  it("empties the cache, enters the demo and opens the first sample project", async () => {
    queryClient.setQueryData(["me"], f.meBody);
    queryClient.setQueryData(["tenant", "signed-in"], f.tenantBody);
    const calls = stubFetch([{ status: 200, body: demoMe }]);
    const res = await demoLoader();
    expect(isDemo()).toBe(true);
    expect(calls.map((c) => c.url)).toEqual(["/api/demo/me"]);
    expect(res.status).toBe(302);
    expect(res.headers.get("Location")).toBe(`/projects/${f.TENANT_ID}`);
    // the signed-in session's answers are gone
    expect(queryClient.getQueryData(["tenant", "signed-in"])).toBeUndefined();
    expect(queryClient.getQueryData(["me"])).toMatchObject({ demo: true });
  });

  it("leaves the tab as it was when the demo does not open", async () => {
    stubFetch([{ status: 429, body: f.errorBody("rate_limited") }]);
    await expect(demoLoader()).rejects.toBeInstanceOf(ApiError);
    expect(isDemo()).toBe(false);
    expect(queryClient.getQueryData(["me"])).toBeUndefined();
  });
});

describe("demo console", () => {
  // answers the demo routes the shell reads; any other request fails the test,
  // so a page that asks a route the demo refuses shows up here
  function serveDemo() {
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
        if (input === "/api/demo/me") return json(200, demoMe);
        if (input === `/api/demo/tenants/${f.TENANT_ID}`)
          return json(200, { ...f.tenantBody, role: "viewer" });
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
        { path: "projects/:tenantId/*", ...page },
        { path: "operator/*", ...page },
      ],
    },
  ];

  it("carries the banner and lists only the readable project pages", async () => {
    enterDemo();
    const urls = serveDemo();
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent).toContain("page body"));
    const banner = document.querySelector("[data-demo-banner]");
    expect(banner?.textContent).toBe("Demo with sample data. Read-only.Enroll your project");
    expect(banner?.querySelector("a")?.getAttribute("href")).toBe("/sign-in");
    const nav = [...host.querySelectorAll("aside nav a")].map((a) => a.textContent);
    expect(nav).toEqual(["Overview", "Web", "API", "On-chain", "SCF review", "Sites"]);
    // no account menu: the demo has no account and nothing to sign out of
    expect(host.querySelector("aside")?.textContent).not.toContain("Demo");
    expect(urls.every((u) => u.startsWith("/api/demo/"))).toBe(true);
  });

  it("answers a page the demo cannot draw with not found", async () => {
    enterDemo();
    serveDemo();
    const host = await renderRoutes(routes, `/projects/${f.TENANT_ID}/members`);
    await waitFor(() => expect(host.textContent).toContain("Page not found"));
    expect(host.textContent).not.toContain("page body");
  });

  it("opens the operator area on its project list, with no step-up", async () => {
    enterDemo();
    const urls = serveDemo();
    const host = await renderRoutes(routes, "/operator");
    await waitFor(() => expect(host.textContent).toContain("page body"));
    const nav = [...host.querySelectorAll("aside nav a")].map((a) => a.textContent);
    expect(nav).toEqual(["All projects", "Rules"]);
    expect(urls.some((u) => u.includes("step-up"))).toBe(false);
  });

  it("gives the project pages a read-only context for the viewer role", async () => {
    enterDemo();
    serveDemo();
    function Probe() {
      const { readOnly, isOwner } = useProject();
      return <p data-read-only={String(readOnly)} data-owner={String(isOwner)} />;
    }
    const host = await renderRoutes(
      [
        {
          path: "/projects/:tenantId",
          element: <ProjectLayout />,
          children: [{ index: true, element: <Probe /> }],
        },
      ],
      `/projects/${f.TENANT_ID}`,
    );
    await waitFor(() => expect(host.querySelector("[data-read-only]")).not.toBeNull());
    expect(host.querySelector("[data-read-only]")?.getAttribute("data-read-only")).toBe("true");
    expect(host.querySelector("[data-owner]")?.getAttribute("data-owner")).toBe("false");
  });
});
