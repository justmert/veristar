import { describe, expect, it, vi } from "vitest";
import { auditPath, type TenantListQuery, tenantListPath } from "@/api/endpoints";
import { tenantAction } from "@/api/schemas";
import { AuditPage, auditSummary, dayToUtc } from "@/pages/operator/audit";
import { OperatorLayout } from "@/pages/operator/layout";
import { ReviewQueuePage } from "@/pages/operator/review";
import { SettingsPage } from "@/pages/operator/settings";
import { TenantDetailPage } from "@/pages/operator/tenant-detail";
import { queryFromParams, TenantsPage } from "@/pages/operator/tenants";
import * as f from "./fixtures/contract";
import { integrityReportBody } from "./fixtures/global";
import { byLabel, byRole, click, renderRoutes, stubFetch, type, waitFor } from "./helpers";

const detailRoute = [{ path: "/operator/tenants/:tenantId", element: <TenantDetailPage /> }];

// the on-chain section of the detail reads its own route after the detail
// 19.1 a project without a cohort row
const noCohort = {
  status: 200,
  body: {
    tenant_id: f.TENANT_ID,
    confirmed: null,
    confirmed_at: null,
    proposal: null,
    proposed_at: null,
  },
};

const noOnchain = {
  status: 200,
  body: {
    from: "2026-10-18",
    to: "2026-11-16",
    days: [],
    sources: { accounts: "horizon", contracts: "rpc_events", account_backfill_days: 90 },
    totals: {
      ids: 0,
      ids_verified: 0,
      accounts: 0,
      contracts: 0,
      operations: 0,
      transactions: 0,
      days_missing: 0,
      weekly_active_accounts: { average: null, latest: null, weeks_counted: 0, weeks_missing: 0 },
    },
    ids: [],
    weeks: [],
    link: { two_way: false, sites: [] },
  },
};

function dialogOpen(host: HTMLElement): HTMLDialogElement | undefined {
  return [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
}

describe("operator area guard", () => {
  it("shows a non-operator the operators-only page and calls no operator route", async () => {
    const calls = stubFetch([{ status: 200, body: f.meBody }]);
    const host = await renderRoutes(
      [
        {
          path: "/operator",
          element: <OperatorLayout />,
          children: [{ index: true, element: <p>queue</p> }],
        },
      ],
      "/operator",
    );
    await waitFor(() => expect(host.textContent).toContain("Operators only"));
    expect(host.textContent).not.toContain("queue");
    expect(calls.map((c) => c.url)).toEqual(["/api/me"]);
  });

  it("opens the area for an operator", async () => {
    // the area polls the alerts for its nav badge and critical banner
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      { status: 200, body: f.steppedUpBody },
      {
        status: 200,
        body: {
          checked_at: "2026-11-13T12:00:00Z",
          watchdog_firing: true,
          sources: [
            { address: "10.0.1.4:9093", reachable: true, watchdog_firing: true, error: null },
          ],
          alerts: [],
        },
      },
    ]);
    const host = await renderRoutes(
      [
        {
          path: "/operator",
          element: <OperatorLayout />,
          children: [{ index: true, element: <p>queue</p> }],
        },
      ],
      "/operator",
    );
    await waitFor(() => expect(host.textContent).toContain("queue"));
    // the place header and its tabs; the sidebar of the shell picks the place
    expect(host.querySelector("h1")?.textContent).toBe("Attention");
    const tabs = host.querySelector('nav[aria-label="Operator sections"]') as HTMLElement;
    expect([...tabs.querySelectorAll("a")].map((a) => a.textContent)).toEqual([
      "Review queue",
      "Alerts",
    ]);
  });
});

describe("operator project detail", () => {
  it("shows enrollment answers, evidence, ingest counts and only the allowed actions", async () => {
    stubFetch([
      { status: 200, body: f.operatorDetailBody },
      { status: 200, body: integrityReportBody },
      noOnchain,
      noCohort,
    ]);
    const host = await renderRoutes(detailRoute, `/operator/tenants/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent).toContain("Soroban AMM on mainnet"));
    await waitFor(() =>
      expect(host.textContent).toContain("The project declares no accounts or contracts."),
    );
    // the integrity report of the project, with the reasons of its days
    await waitFor(() =>
      expect(host.textContent).toContain("15% of accepted events went to the quarantine band"),
    );
    expect(host.textContent).toContain("not a valid id");
    expect(host.textContent).toContain("Domain verified");
    expect(host.textContent).toContain("Public network");
    expect(host.textContent).toContain("2,810");
    expect(host.textContent).toContain("90,210");
    expect(host.textContent).toContain("Reason: Spam traffic");
    const actions = host.querySelector("section[aria-label=Actions]");
    const labels = [...(actions?.querySelectorAll("button") ?? [])]
      .filter((b) => !b.closest("dialog"))
      .map((b) => b.textContent);
    expect(labels).toEqual(["Approve", "Reject", "Remove"]);
  });

  it("requires a reason to reject and posts it with the csrf header", async () => {
    const calls = stubFetch([
      { status: 200, body: f.operatorDetailBody },
      { status: 200, body: integrityReportBody },
      noOnchain,
      noCohort,
      {
        status: 200,
        body: { ...f.operatorSummaryBody, status: "rejected", status_reason: "Not on Stellar" },
      },
    ]);
    const host = await renderRoutes(detailRoute, `/operator/tenants/${f.TENANT_ID}`);
    await waitFor(() => byRole(host, "button", "Reject"));
    await waitFor(() => expect(calls).toHaveLength(4));
    await click(byRole(host, "button", "Reject"));
    const dialog = dialogOpen(host);
    expect(dialog?.textContent).toContain("Reject Stellar DEX?");
    // the dialog's own confirm button
    const confirm = [...(dialog?.querySelectorAll("button") ?? [])].find(
      (b) => b.textContent === "Reject",
    ) as HTMLElement;
    await waitFor(() => expect(calls).toHaveLength(4));
    await click(confirm);
    expect(calls).toHaveLength(4);
    expect(dialog?.textContent).toContain("Required.");
    await type(byLabel(dialog as HTMLElement, "Reason"), "  Not on Stellar ");
    await click(confirm);
    await waitFor(() => expect(calls).toHaveLength(5));
    expect(calls[4]?.method).toBe("POST");
    expect(calls[4]?.url).toBe(`/api/operator/tenants/${f.TENANT_ID}/reject`);
    expect(calls[4]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[4]?.body).toEqual({ reason: "Not on Stellar" });
    await waitFor(() => expect(dialogOpen(host)).toBeUndefined());
  });

  it("approves without a reason by sending an empty body", async () => {
    const calls = stubFetch([
      { status: 200, body: f.operatorDetailBody },
      { status: 200, body: integrityReportBody },
      noOnchain,
      noCohort,
      { status: 200, body: { ...f.operatorSummaryBody, status: "active" } },
    ]);
    const host = await renderRoutes(detailRoute, `/operator/tenants/${f.TENANT_ID}`);
    await waitFor(() => byRole(host, "button", "Approve"));
    await waitFor(() => expect(calls).toHaveLength(4));
    await click(byRole(host, "button", "Approve"));
    const dialog = dialogOpen(host) as HTMLElement;
    const confirm = [...dialog.querySelectorAll("button")].find((b) => b.textContent === "Approve");
    await click(confirm as HTMLElement);
    await waitFor(() => expect(calls).toHaveLength(5));
    expect(calls[4]?.url).toBe(`/api/operator/tenants/${f.TENANT_ID}/approve`);
    expect(calls[4]?.body).toEqual({});
  });

  it("shows a 409 invalid_transition in the dialog", async () => {
    const calls = stubFetch([
      { status: 200, body: f.operatorDetailBody },
      { status: 200, body: integrityReportBody },
      noOnchain,
      noCohort,
      { status: 409, body: f.errorBody("invalid_transition") },
    ]);
    const host = await renderRoutes(detailRoute, `/operator/tenants/${f.TENANT_ID}`);
    await waitFor(() => byRole(host, "button", "Remove"));
    await waitFor(() => expect(calls).toHaveLength(4));
    await click(byRole(host, "button", "Remove"));
    const dialog = dialogOpen(host) as HTMLElement;
    await type(byLabel(dialog, "Reason"), "Fraud");
    const confirm = [...dialog.querySelectorAll("button")].find((b) => b.textContent === "Remove");
    await click(confirm as HTMLElement);
    await waitFor(() =>
      expect(dialog.textContent).toContain("not possible from the current status"),
    );
  });

  it("shows removal progress and no actions for a removed project", async () => {
    stubFetch([
      { status: 200, body: f.removedDetailBody },
      { status: 200, body: integrityReportBody },
      noOnchain,
      noCohort,
    ]);
    const host = await renderRoutes(detailRoute, `/operator/tenants/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent).toContain("Data is still being deleted."));
    expect(host.textContent).toContain("registry");
    expect(host.textContent).toContain("No action is possible from this status.");
  });

  it("answers 404 for an unknown project", async () => {
    stubFetch([{ status: 404, body: f.errorBody("not_found") }]);
    const host = await renderRoutes(detailRoute, `/operator/tenants/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent?.toLowerCase()).toContain("not found"));
  });
});

describe("operator project list", () => {
  it("builds the contract's query from the filters", () => {
    const q: TenantListQuery = {
      status: ["pending", "suspended"],
      review: true,
      q: " dex ",
      sort: "name",
      order: "asc",
    };
    expect(tenantListPath(q, "abc")).toBe(
      "/operator/tenants?status=pending%2Csuspended&review=true&q=dex&sort=name&order=asc&limit=50&cursor=abc",
    );
    expect(queryFromParams(new URLSearchParams("status=pending,bogus&sort=evil&order=up"))).toEqual(
      {
        status: ["pending"],
        review: false,
        q: "",
        sort: "created_at",
        order: "desc",
      },
    );
  });

  it("filters by status, searches and loads the next page", async () => {
    const calls = stubFetch([
      { status: 200, body: f.operatorSitePageBody },
      { status: 200, body: f.operatorSitePageBody },
      { status: 200, body: f.operatorSitePageBody },
      {
        status: 200,
        body: {
          items: [{ ...f.operatorSummaryBody, id: f.REMOVED_TENANT_ID, name: "Second" }],
          next_cursor: null,
        },
      },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator/tenants", element: <TenantsPage /> }],
      "/operator/tenants",
    );
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    expect(calls[0]?.url).toBe("/api/operator/tenants?sort=created_at&order=desc&limit=50");
    const status = host.querySelector("fieldset") as HTMLElement;
    const pending = byRole(status, "button", "Pending");
    await click(pending);
    await waitFor(() => expect(pending.getAttribute("aria-pressed")).toBe("true"));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.url).toContain("status=pending");
    await type(byLabel(host, "Search"), "dex");
    await click(byRole(host, "button", "Search"));
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[2]?.url).toContain("q=dex");
    await waitFor(() => byRole(host, "button", "Load more"));
    await click(byRole(host, "button", "Load more"));
    await waitFor(() => expect(host.textContent).toContain("Second"));
    expect(calls[3]?.url).toContain("cursor=Y3Vyc29y");
  });
});

describe("enrollment mode switch", () => {
  it("asks for confirmation and puts the new mode", async () => {
    const calls = stubFetch([
      { status: 200, body: { mode: "open" } },
      { status: 200, body: { keep: false } },
      { status: 200, body: { mode: "approval" } },
    ]);
    const host = await renderRoutes([{ path: "/", element: <SettingsPage /> }], "/");
    await waitFor(() => byLabel(host, "Approval required"));
    expect(host.textContent).toContain("Current mode: Open");
    // the change button is live only once another option is chosen
    const change = () =>
      byRole(
        host.querySelector("[aria-labelledby=mode-h]") as HTMLElement,
        "button",
        "Change setting",
      );
    expect(change().hasAttribute("disabled")).toBe(true);
    await click(byLabel(host, "Approval required") as HTMLElement);
    expect(change().hasAttribute("disabled")).toBe(false);
    await click(change());
    const dialog = dialogOpen(host) as HTMLElement;
    expect(dialog.textContent).toContain("new enrollments only");
    expect(calls).toHaveLength(2);
    await click(byRole(dialog, "button", "Switch mode"));
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[2]?.method).toBe("PUT");
    expect(calls[2]?.url).toBe("/api/operator/settings/enrollment-mode");
    expect(calls[2]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[2]?.body).toEqual({ mode: "approval" });
  });

  it("cancelling the dialog sends nothing", async () => {
    const calls = stubFetch([
      { status: 200, body: { mode: "approval" } },
      { status: 200, body: { keep: true } },
    ]);
    const host = await renderRoutes([{ path: "/", element: <SettingsPage /> }], "/");
    await waitFor(() => byLabel(host, "Open"));
    await click(byLabel(host, "Open") as HTMLElement);
    await click(
      byRole(
        host.querySelector("[aria-labelledby=mode-h]") as HTMLElement,
        "button",
        "Change setting",
      ),
    );
    await click(byRole(dialogOpen(host) as HTMLElement, "button", "Cancel"));
    expect(dialogOpen(host)).toBeUndefined();
    expect(calls).toHaveLength(2);
  });
});

describe("audit log", () => {
  it("reads utc days and builds the filter query", () => {
    expect(dayToUtc("2026-11-12", false)).toBe("2026-11-12T00:00:00Z");
    expect(dayToUtc("2026-11-12", true)).toBe("2026-11-13T00:00:00Z");
    expect(
      auditPath(
        { tenant_id: f.TENANT_ID, action: "tenant.", actor: "", target: "", from: "", to: "" },
        null,
      ),
    ).toBe(`/operator/audit?tenant_id=${f.TENANT_ID}&action=tenant.&limit=50`);
  });

  it("summarises status changes and reasons", () => {
    expect(auditSummary(f.auditEntryBody)).toEqual([
      "Status active to suspended",
      "Reason: Spam traffic",
    ]);
  });

  it("applies filters from the form", async () => {
    const calls = stubFetch([
      { status: 200, body: f.auditPageBody },
      { status: 200, body: f.auditPageBody },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator/audit", element: <AuditPage /> }],
      "/operator/audit",
    );
    await waitFor(() => expect(host.textContent).toContain("umami.password_rotate"));
    expect(host.textContent).toContain("sa-operator");
    await type(byLabel(host, "Action"), "tenant.");
    await type(byLabel(host, "From (UTC day)"), "2026-11-01");
    await click(byRole(host, "button", "Apply filters"));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.url).toBe(
      "/api/operator/audit?action=tenant.&from=2026-11-01T00%3A00%3A00Z&limit=50",
    );
  });
});

it("knows every lifecycle action of contract 5.5", () => {
  expect(tenantAction.options).toEqual(["approve", "reject", "suspend", "resume", "remove"]);
});

describe("review workspace", () => {
  const noAlerts = {
    checked_at: "2026-11-13T12:00:00Z",
    watchdog_firing: true,
    sources: [{ address: "10.0.1.4:9093", reachable: true, watchdog_firing: true, error: null }],
    alerts: [],
  };
  const pendingDetail = {
    ...f.operatorDetailBody,
    tenant: { ...f.operatorDetailBody.tenant, status: "pending" },
    allowed_actions: ["approve", "reject", "remove"],
  };

  it("shows the queue, the evidence and rejects with a required reason", async () => {
    const calls = stubFetch([
      { status: 200, body: noAlerts },
      {
        status: 200,
        body: { items: [{ ...f.operatorSummaryBody, status: "pending" }], next_cursor: null },
      },
      { status: 200, body: pendingDetail },
      { status: 200, body: { ...f.operatorSummaryBody, status: "rejected" } },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator", element: <ReviewQueuePage /> }],
      "/operator",
    );
    await waitFor(() => expect(host.textContent).toContain("Soroban AMM on mainnet"));
    expect(calls[1]?.url).toBe(
      "/api/operator/tenants?review=true&sort=created_at&order=asc&limit=50",
    );
    expect(host.textContent).toContain("No alert needs action.");
    expect(host.querySelector("ul[aria-label=Evidence]")?.textContent).toContain(
      "1 of 2 not a valid id",
    );

    const reject = [...host.querySelectorAll("[role=radio]")].find((r) =>
      r.textContent?.includes("Reject"),
    ) as HTMLElement;
    await click(reject);
    await click(byRole(host, "button", "Reject projectCtrl Enter"));
    expect(calls).toHaveLength(3);
    expect(host.textContent).toContain("Required.");
    await type(byLabel(host, "Reason"), " Not on Stellar ");
    await click(byRole(host, "button", "Reject projectCtrl Enter"));
    await waitFor(() => expect(calls).toHaveLength(4));
    expect(calls[3]?.url).toBe(`/api/operator/tenants/${f.TENANT_ID}/reject`);
    expect(calls[3]?.body).toEqual({ reason: "Not on Stellar" });
  });
});

describe("operator step-up", () => {
  const area = [
    {
      path: "/operator",
      element: <OperatorLayout />,
      children: [{ index: true, element: <p>queue</p> }],
    },
  ];
  const notSteppedUp = {
    stepped_up: false,
    stepped_up_at: null,
    expires_at: null,
    max_age_seconds: 43200,
  };

  it("asks an operator with a passkey to confirm before the area loads", async () => {
    vi.stubGlobal("PublicKeyCredential", class {});
    const calls = stubFetch([
      { status: 200, body: f.operatorMeBody },
      {
        status: 200,
        body: { ...notSteppedUp, passkeys: 2, enrollment: "closed", enrollment_until: null },
      },
    ]);
    const host = await renderRoutes(area, "/operator");
    await waitFor(() => expect(host.textContent).toContain("Use passkey or security key"));
    expect(host.textContent).not.toContain("queue");
    // nothing of the area is asked for before the step-up
    expect(calls.map((c) => c.url)).toEqual(["/api/me", "/api/operator/step-up"]);
  });

  it("offers the first registration inside the enrollment window", async () => {
    vi.stubGlobal("PublicKeyCredential", class {});
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      {
        status: 200,
        body: {
          ...notSteppedUp,
          passkeys: 0,
          enrollment: "open",
          enrollment_until: "2026-11-17T09:00:00Z",
        },
      },
    ]);
    const host = await renderRoutes(area, "/operator");
    await waitFor(() => expect(host.textContent).toContain("Register a passkey or security key"));
    expect(host.textContent).toContain("Nov 17, 2026");
  });

  it("sends an operator whose window needs a newer sign-in to sign in again", async () => {
    vi.stubGlobal("PublicKeyCredential", class {});
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      {
        status: 200,
        body: {
          ...notSteppedUp,
          passkeys: 0,
          enrollment: "sign_in_required",
          enrollment_until: null,
        },
      },
    ]);
    const host = await renderRoutes(area, "/operator");
    await waitFor(() =>
      expect(host.textContent).toContain("Sign in again to register your first passkey."),
    );
  });

  it("says so when the browser has no webauthn", async () => {
    vi.stubGlobal("PublicKeyCredential", undefined);
    stubFetch([
      { status: 200, body: f.operatorMeBody },
      {
        status: 200,
        body: { ...notSteppedUp, passkeys: 1, enrollment: "closed", enrollment_until: null },
      },
    ]);
    const host = await renderRoutes(area, "/operator");
    await waitFor(() =>
      expect(host.textContent).toContain("This browser cannot use passkeys or security keys."),
    );
  });
});
