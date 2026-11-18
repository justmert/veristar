import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { auditPath, type TenantListQuery, tenantListPath } from "@/api/endpoints";
import { tenantAction } from "@/api/schemas";
import {
  actorKind,
  actorName,
  auditChanges,
  auditDaysError,
  auditSentence,
  auditVerb,
  dayToUtc,
  foldRuns,
} from "@/lib/audit";
import { dayPairError, EMPTY_FILTERS } from "@/lib/global";
import { statusQuery } from "@/lib/operator-projects";
import { AuditPage } from "@/pages/operator/audit";
import { OperatorLayout } from "@/pages/operator/layout";
import { AddOperator, validateAddOperator } from "@/pages/operator/operators";
import { OperatorProjectPage } from "@/pages/operator/project";
import { OperatorProjectsPage } from "@/pages/operator/projects";
import { ReviewQueuePage } from "@/pages/operator/review";
import { SettingsPage } from "@/pages/operator/settings";
import * as f from "./fixtures/contract";
import { apiCompareBody, integrityReportBody, webCompareBody } from "./fixtures/global";
import { byLabel, byRole, click, renderRoutes, stubFetch, type, waitFor } from "./helpers";
import { type Route, routeFetch } from "./route-fetch";

const detailRoute = [{ path: "/operator/projects/:tenantId", element: <OperatorProjectPage /> }];

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

// the one-project comparison the review and the project page read their
// numbers from
const oneWeb = { ...webCompareBody, projects: [webCompareBody.projects[0]] };
const oneApi = { ...apiCompareBody, projects: [apiCompareBody.projects[0]] };

const TENANT = `/api/operator/tenants/${f.TENANT_ID}`;

function detailRoutes(detail: unknown, extra: Route[] = []): Route[] {
  return [
    ...extra,
    { path: TENANT, answer: { status: 200, body: detail } },
    { path: "/api/operator/web/integrity", answer: { status: 200, body: integrityReportBody } },
    { path: `${TENANT}/onchain`, answer: noOnchain },
    { path: `${TENANT}/cohort`, answer: noCohort },
    { path: "/api/operator/web/compare", answer: { status: 200, body: oneWeb } },
    { path: "/api/operator/api/compare", answer: { status: 200, body: oneApi } },
  ];
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
    // each page draws its own header; the gate adds none and no tab bar
    expect(host.querySelector("h1")).toBeNull();
    expect(host.querySelector("nav")).toBeNull();
  });
});

describe("operator project page", () => {
  it("shows the decision, the numbers, the evidence once and the record", async () => {
    routeFetch(detailRoutes(f.operatorDetailBody));
    const host = await renderRoutes(detailRoute, `/operator/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(host.querySelector("h1")?.textContent).toBe("Stellar DEX"));
    expect(host.textContent).toContain("Soroban AMM on mainnet");
    expect(host.textContent).toContain("Not a valid id");
    expect(host.textContent).toContain("Reason: Spam traffic");
    expect(host.querySelectorAll("ul[aria-label=Evidence]")).toHaveLength(1);
    // the numbers come from the comparison of this one project
    await waitFor(() =>
      expect(host.querySelector("[data-stat=requests]")?.textContent).toContain("300"),
    );
    expect(host.querySelector("[data-stat=grade]")?.textContent).toContain("C");
    // the decision is the one panel; remove sits in the actions menu
    expect(host.querySelector("form[aria-label='Decision on Stellar DEX']")).not.toBeNull();
    expect(byRole(host, "button", "Actions")).toBeTruthy();
    // the last entries of the audit log read as sentences
    expect(host.textContent).toContain("sa-operator suspended a project");
    expect(host.querySelector("a[href*='/operator/audit?tenant_id=']")).not.toBeNull();
  });

  it("removes through the menu with a reason and the typed slug", async () => {
    const calls = routeFetch(
      detailRoutes(f.operatorDetailBody, [
        {
          method: "POST",
          path: `${TENANT}/remove`,
          answer: { status: 409, body: f.errorBody("invalid_transition") },
        },
      ]),
    );
    const host = await renderRoutes(detailRoute, `/operator/projects/${f.TENANT_ID}`);
    await waitFor(() => byRole(host, "button", "Actions"));
    const trigger = byRole(host, "button", "Actions");
    await act(async () => {
      trigger.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 }));
    });
    const item = () =>
      [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find(
        (m) => m.textContent === "Remove project",
      );
    await waitFor(() => expect(item()).toBeTruthy());
    await click(item() as HTMLElement);
    const dialog = dialogOpen(host) as HTMLElement;
    expect(dialog.textContent).toContain("Remove Stellar DEX?");
    const confirm = [...dialog.querySelectorAll("button")].find(
      (b) => b.textContent === "Remove project",
    ) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    await type(byLabel(dialog, "Reason"), "Fraud");
    await type(byLabel(dialog, "Type stellar-dex to confirm"), "stellar-dex");
    await click(confirm);
    await waitFor(() =>
      expect(dialog.textContent).toContain("not possible from the current status"),
    );
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toBe(`${TENANT}/remove`);
    expect(post?.body).toEqual({ reason: "Fraud" });
  });

  it("shows the removal steps in words and no decision for a removed project", async () => {
    routeFetch(detailRoutes(f.removedDetailBody));
    const host = await renderRoutes(detailRoute, `/operator/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent).toContain("The service is deleting the data."));
    expect(host.textContent).toContain("Project record closed");
    expect(host.querySelector("form[aria-label^='Decision on']")).toBeNull();
  });

  it("keeps the header when the project does not load", async () => {
    routeFetch([{ path: TENANT, answer: { status: 500, body: f.errorBody("internal") } }]);
    const host = await renderRoutes(detailRoute, `/operator/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent).toContain("The project did not load"));
    expect(host.querySelector("h1")?.textContent).toBe("Project");
  });

  it("answers 404 for an unknown project", async () => {
    stubFetch([{ status: 404, body: f.errorBody("not_found") }]);
    const host = await renderRoutes(detailRoute, `/operator/projects/${f.TENANT_ID}`);
    await waitFor(() => expect(host.textContent?.toLowerCase()).toContain("not found"));
  });
});

describe("operator project list, status view", () => {
  it("builds the contract's query from the address", () => {
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
    expect(statusQuery(new URLSearchParams("status=pending,bogus&sort=evil&order=up"))).toEqual({
      status: ["pending"],
      review: false,
      q: "",
      sort: "created_at",
      order: "desc",
    });
  });

  it("searches as the person types, sorts by a header and loads the next page", async () => {
    const calls = routeFetch([
      {
        path: "/api/operator/tenants",
        answer: [
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
        ],
      },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator/projects", element: <OperatorProjectsPage /> }],
      "/operator/projects",
    );
    await waitFor(() => expect(host.textContent).toContain("Stellar DEX"));
    expect(calls[0]?.url).toBe("/api/operator/tenants?sort=created_at&order=desc&limit=50");
    await type(byLabel(host, "Search projects"), "dex");
    await waitFor(() => expect(calls.some((c) => c.url.includes("q=dex"))).toBe(true));
    const name = host.querySelector("th[aria-sort] button") as HTMLElement;
    await click(name);
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("sort=name&order=asc"))).toBe(true),
    );
    await waitFor(() => byRole(host, "button", "Load more"));
    await click(byRole(host, "button", "Load more"));
    await waitFor(() => expect(host.textContent).toContain("Second"));
    expect(calls.at(-1)?.url).toContain("cursor=Y3Vyc29y");
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

  it("spells out lists and objects in change chips", () => {
    const e = {
      ...f.auditEntryBody,
      detail: {
        origins: { old: ["https://a.example"], new: ["https://a.example", "https://b.example"] },
        cohort: { old: null, new: { program: "Build Award", round: 38 } },
        campaign_params: { old: false, new: true },
        names: { old: [], new: "" },
      },
    };
    expect(auditChanges(e)).toEqual([
      { key: "origins", from: "https://a.example", to: "https://a.example, https://b.example" },
      { key: "cohort", from: "–", to: "program Build Award, round 38" },
      { key: "campaign params", from: "off", to: "on" },
      { key: "names", from: "–", to: "–" },
    ]);
    expect(JSON.stringify(auditChanges(e))).not.toContain("[object Object]");
  });

  it("reads an entry as a sentence and tells the system from people", () => {
    const person = auditSentence(f.auditEntryBody);
    expect(person).toMatchObject({
      actor: "sa-operator",
      kind: "person",
      verb: "suspended",
      object: "a project",
      href: `/operator/projects/${f.TENANT_ID}`,
    });
    const system = f.auditPageBody.items[1] as typeof f.auditEntryBody;
    expect(actorKind(system)).toBe("system");
    expect(actorName(system)).toBe("The system (umami rotate)");
    expect(auditSentence(system).object).toBeNull();
    expect(actorName({ ...system, actor: "mcp_credential:x" })).toBe("An AI assistant");
    expect(actorName({ ...system, actor: `user:${f.TENANT_ID}` })).toBe("A person");
  });

  it("folds a run of the same system event, never the actions of people", () => {
    const run = Array.from({ length: 4 }, (_, i) => ({
      ...f.auditEntryBody,
      id: 100 - i,
      actor: "system:verify-domains",
      actor_login: null,
      action: "site.verify",
    }));
    const people = Array.from({ length: 3 }, (_, i) => ({ ...f.auditEntryBody, id: 10 - i }));
    const items = foldRuns([...run, ...people]);
    expect(items.map((i) => (i.kind === "run" ? `run ${i.entries.length}` : "one"))).toEqual([
      "run 4",
      "one",
      "one",
      "one",
    ]);
    expect(foldRuns(run.slice(0, 2)).every((i) => i.kind === "one")).toBe(true);
  });

  it("applies the filters as they change, with no Apply button", async () => {
    const calls = routeFetch([
      { path: "/api/operator/audit", answer: { status: 200, body: f.auditPageBody } },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator/audit", element: <AuditPage /> }],
      "/operator/audit",
    );
    await waitFor(() => expect(host.textContent).toContain("sa-operator suspended a project"));
    expect(host.querySelector("[data-actor=system]")).not.toBeNull();
    expect(host.textContent).not.toContain("Apply");
    const action = byLabel(host, "Action") as unknown as HTMLSelectElement;
    await act(async () => {
      action.value = "tenant.";
      action.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.url).toBe("/api/operator/audit?action=tenant.&limit=50");
    await type(byLabel(host, "From"), "2026-11-01");
    await waitFor(() => expect(calls).toHaveLength(3));
    expect(calls[2]?.url).toBe(
      "/api/operator/audit?action=tenant.&from=2026-11-01T00%3A00%3A00Z&limit=50",
    );
    expect(host.querySelector("[data-count]")?.textContent).toBe("2 entries shown.");
  });
});

it("knows every lifecycle action of contract 5.5", () => {
  expect(tenantAction.options).toEqual(["approve", "reject", "suspend", "resume", "remove"]);
});

describe("review workspace", () => {
  const pendingDetail = {
    ...f.operatorDetailBody,
    tenant: { ...f.operatorDetailBody.tenant, status: "pending" },
    allowed_actions: ["approve", "reject", "remove"],
  };

  it("shows the queue, the numbers and the evidence, and rejects with a required reason", async () => {
    const calls = routeFetch(
      detailRoutes(pendingDetail, [
        {
          path: "/api/operator/tenants",
          answer: {
            status: 200,
            body: { items: [{ ...f.operatorSummaryBody, status: "pending" }], next_cursor: null },
          },
        },
        {
          method: "POST",
          path: `${TENANT}/reject`,
          answer: { status: 200, body: { ...f.operatorSummaryBody, status: "rejected" } },
        },
      ]),
    );
    const host = await renderRoutes(
      [{ path: "/operator", element: <ReviewQueuePage /> }],
      "/operator",
    );
    await waitFor(() => expect(host.textContent).toContain("Soroban AMM on mainnet"));
    expect(calls[0]?.url).toBe(
      "/api/operator/tenants?review=true&sort=created_at&order=asc&limit=50",
    );
    expect(host.querySelector("h1")?.textContent).toBe("Review");
    // no alerts here: the alerts have their own page
    expect(calls.some((c) => c.url.includes("/alerts"))).toBe(false);
    expect(host.querySelector("ul[aria-label=Evidence]")?.textContent).toContain(
      "1 of 2 not a valid id",
    );
    await waitFor(() =>
      expect(host.querySelector("[data-stat=pageviews]")?.textContent).not.toContain("Loading"),
    );

    const radios = [...host.querySelectorAll<HTMLElement>("[role=radio]")];
    const reject = radios.find((r) => r.textContent?.includes("Reject")) as HTMLElement;
    const approve = radios.find((r) => r.textContent?.includes("Approve")) as HTMLElement;
    // one tab stop; the arrow keys move the choice and the focus
    expect(approve.tabIndex).toBe(0);
    expect(reject.tabIndex).toBe(-1);
    approve.focus();
    await act(async () => {
      approve.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    });
    expect(reject.getAttribute("aria-checked")).toBe("true");
    expect(document.activeElement).toBe(reject);
    await act(async () => {
      reject.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    });
    expect(approve.getAttribute("aria-checked")).toBe("true");
    await click(reject);
    await click(byRole(host, "button", "Reject projectCtrl Enter"));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(host.textContent).toContain("Required.");
    await type(byLabel(host, "Reason"), " Not on Stellar ");
    await click(byRole(host, "button", "Reject projectCtrl Enter"));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toBe(`${TENANT}/reject`);
    expect(post?.body).toEqual({ reason: "Not on Stellar" });
  });

  it("says so when nothing waits", async () => {
    routeFetch([
      {
        path: "/api/operator/tenants",
        answer: { status: 200, body: { items: [], next_cursor: null } },
      },
    ]);
    const host = await renderRoutes(
      [{ path: "/operator", element: <ReviewQueuePage /> }],
      "/operator",
    );
    await waitFor(() => expect(host.textContent).toContain("Nothing to review"));
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
    sign_in_passkey: false,
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
    await waitFor(() => expect(host.textContent).toContain("Use your passkey"));
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
    await waitFor(() => expect(host.textContent).toContain("Register a passkey"));
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
    await waitFor(() => expect(host.textContent).toContain("This browser cannot use passkeys."));
  });
});

describe("operator day pairs", () => {
  it("refuse an end before its start before asking", () => {
    expect(auditDaysError("2026-11-10", "2026-11-01")).toBe("End on or after the start day.");
    expect(auditDaysError("2025-01-01", "2026-11-01")).toBe("Choose at most 400 days.");
    expect(auditDaysError("2026-11-01", "2026-11-01")).toBeNull();
    expect(auditDaysError("", "2026-11-01")).toBeNull();
    expect(
      dayPairError({ ...EMPTY_FILTERS, award_from: "2026-05-01", award_to: "2026-04-01" }),
    ).toBe("Awarded to is before Awarded from. Change one of them.");
    expect(
      dayPairError({ ...EMPTY_FILTERS, enrolled_from: "2026-05-01", enrolled_to: "2026-04-01" }),
    ).toContain("Enrolled to");
    expect(dayPairError(EMPTY_FILTERS)).toBeNull();
  });
});

describe("audit sentences", () => {
  it("words the operator, cohort, rule, report, on-chain, export and identity actions", () => {
    expect(auditVerb("operator.add")).toBe("added the operator");
    expect(auditVerb("cohort.confirm")).toBe("confirmed the cohort of");
    expect(auditVerb("rule.delete")).toBe("deleted the rule");
    expect(auditVerb("report.revoke")).toBe("revoked a report link of");
    expect(auditVerb("onchain.verify")).toBe("verified an on-chain id of");
    expect(auditVerb("export.csv")).toBe("exported a CSV file of");
    expect(auditVerb("user.identity_link")).toBe("added a sign-in method to");
    expect(auditVerb("site.stellar_toml")).toBe("checked the stellar.toml of the site");
    expect(auditVerb("something.new")).toBe("recorded the action something.new on");
  });
});

describe("adding an operator (contract 5.16)", () => {
  it("checks the identity and the reason before asking", () => {
    expect(validateAddOperator({ kind: "github", value: "", reason: "" })).toEqual({
      identity: "Required.",
      reason: "Enter a reason. It is recorded in the audit log.",
    });
    expect(validateAddOperator({ kind: "stellar", value: "GABC", reason: "x" }).identity).toBe(
      "Not a valid account (G...), muxed account (M...) or contract (C...) id.",
    );
    expect(
      validateAddOperator({ kind: "github", value: "octocat", reason: "a".repeat(501) }).reason,
    ).toBe("Use at most 500 characters.");
    expect(validateAddOperator({ kind: "github", value: "octocat", reason: " on call " })).toEqual(
      {},
    );
  });

  it("shows an empty reason at its field and sends nothing", async () => {
    const calls = stubFetch([]);
    const host = await renderRoutes([{ path: "/", element: <AddOperator /> }], "/");
    await type(byLabel(host, "GitHub login"), "octocat");
    await click(byRole(host, "button", "Add operator"));
    expect(host.textContent).toContain("Enter a reason. It is recorded in the audit log.");
    expect(byLabel(host, "Reason").getAttribute("aria-invalid")).toBe("true");
    expect(calls).toEqual([]);
  });

  it("shows the server's field codes at their fields", async () => {
    stubFetch([
      {
        status: 422,
        body: f.errorBody("validation_failed", { github_login: "unknown_github_login" }),
      },
    ]);
    const host = await renderRoutes([{ path: "/", element: <AddOperator /> }], "/");
    await type(byLabel(host, "GitHub login"), "nobody-here");
    await type(byLabel(host, "Reason"), "joins the on-call rotation");
    await click(byRole(host, "button", "Add operator"));
    await waitFor(() => expect(host.textContent).toContain("No GitHub user has this username."));
  });
});
