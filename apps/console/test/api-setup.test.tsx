import { act } from "react";
import { Outlet } from "react-router";
import { describe, expect, it } from "vitest";
import type { Tenant } from "@/api/schemas";
import { ConnectApiPage, lastUsedToken, validateToken } from "@/pages/project/connect-api";
import * as f from "./fixtures/contract";
import { byLabel, byRole, click, renderRoutes, stubFetch, type, waitFor } from "./helpers";

const base = f.tenantBody as Tenant;
const path = `/projects/${f.TENANT_ID}/api`;
const listPath = `/api/tenants/${f.TENANT_ID}/otlp-tokens?limit=200`;

function routes(tenant: Tenant = base, termsCurrent = true) {
  const ctx = { tenant, me: f.meBody, isOwner: true, termsCurrent };
  return [
    {
      path: "/projects/:tenantId",
      element: <Outlet context={ctx} />,
      children: [{ path: "api", element: <ConnectApiPage /> }],
    },
  ];
}

const page = (items: unknown[]) => ({ items, next_cursor: null });

// the create dialog opens from the first step, or the header once a token exists
async function openCreate(host: HTMLElement) {
  const b = opener(host);
  if (!b) throw new Error("no Create token button");
  await click(b);
}

// a Create token button outside the (closed) dialog
function opener(host: HTMLElement): HTMLElement | null {
  return (
    [...host.querySelectorAll("button")].find(
      (b) => b.textContent?.trim() === "Create token" && !b.closest("dialog"),
    ) ?? null
  );
}

function submit(host: HTMLElement): HTMLElement {
  const dialog = [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
  if (!dialog) throw new Error("no open dialog");
  return byRole(dialog as HTMLElement, "button", "Create token");
}

describe("api analytics setup page (contract section 6)", () => {
  it("lists the active tokens with prefix, environment and use", async () => {
    const calls = stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    expect(calls[0]?.url).toBe(listPath);
    expect(host.textContent).toContain("sa_otlp_Ab3x...");
    expect(host.textContent).toContain("production");
    expect(host.textContent).toContain("Never");
    // the plaintext never comes from a list
    expect(host.textContent).not.toContain(f.OTLP_PLAIN);
  });

  it("asks for revoked tokens only when they are to be shown", async () => {
    const revoked = { ...f.otlpTokenBody, status: "revoked", revoked_at: "2026-11-12T10:00:00Z" };
    const calls = stubFetch([
      { status: 200, body: page([f.otlpTokenBody]) },
      { status: 200, body: page([revoked]) },
    ]);
    const host = await renderRoutes(routes(), path);
    // the list starts with an active token; the toggle sits above it
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    const label = [...host.querySelectorAll("label")].find(
      (l) => l.textContent?.trim() === "Show revoked",
    );
    const toggle = label ? host.ownerDocument.getElementById(label.htmlFor) : null;
    if (!toggle) throw new Error("no show revoked checkbox");
    await click(toggle);
    await waitFor(() => expect(host.textContent).toContain("Revoked"));
    expect(calls[1]?.url).toBe(
      `/api/tenants/${f.TENANT_ID}/otlp-tokens?include_revoked=true&limit=200`,
    );
    // a revoked token has no row menu: nothing is left to do with it
    expect(host.querySelector('button[aria-label^="Actions for"]')).toBeNull();
  });

  it("creates a token and shows the plaintext once, until it is dismissed", async () => {
    const calls = stubFetch([
      { status: 200, body: page([]) },
      { status: 201, body: f.otlpTokenCreatedBody },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(opener(host)).not.toBeNull());
    await openCreate(host);
    await type(byLabel(host, "Token name"), "  Production API ");
    await type(byLabel(host, "Environment"), "production");
    await click(submit(host));
    // the secret is masked until it is shown on purpose
    await waitFor(() => expect(host.querySelector('[aria-label="New secret"]')).not.toBeNull());
    expect(host.textContent).not.toContain(f.OTLP_PLAIN);
    const show = host.querySelector<HTMLButtonElement>('button[aria-label="Show the secret"]');
    if (!show) throw new Error("no show button");
    await click(show);
    await waitFor(() => expect(host.textContent).toContain(f.OTLP_PLAIN));
    expect(calls[1]?.method).toBe("POST");
    expect(calls[1]?.url).toBe(`/api/tenants/${f.TENANT_ID}/otlp-tokens`);
    expect(calls[1]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[1]?.body).toEqual({ name: "Production API", environment: "production" });
    // the exporter of each stack is written for the new token: the copy
    // holds it, the code on screen only its start
    expect(host.querySelector("dialog [data-snippet=node]")?.textContent).toContain(
      `Bearer ${f.OTLP_PLAIN.slice(0, 12)}...`,
    );
    // done stays disabled until the person says they stored it
    const done = byRole(host, "button", "Done") as HTMLButtonElement;
    expect(done.disabled).toBe(true);
    const stored = host.querySelector<HTMLElement>('[aria-label="New secret"] [role="checkbox"]');
    if (!stored) throw new Error("no stored checkbox");
    await click(stored);
    expect(done.disabled).toBe(false);
    await click(done);
    await waitFor(() => expect(host.textContent).not.toContain(f.OTLP_PLAIN));
  });

  it("leaves an empty environment out of the body", async () => {
    const calls = stubFetch([
      { status: 200, body: page([]) },
      { status: 201, body: { ...f.otlpTokenCreatedBody, environment: "" } },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(opener(host)).not.toBeNull());
    await openCreate(host);
    await type(byLabel(host, "Token name"), "Staging");
    await click(submit(host));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.body).toEqual({ name: "Staging" });
  });

  it("checks the fields before sending and shows the server's field codes", async () => {
    const calls = stubFetch([
      { status: 200, body: page([]) },
      { status: 422, body: f.errorBody("validation_failed", { name: "too_many" }) },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(opener(host)).not.toBeNull());
    await openCreate(host);
    await type(byLabel(host, "Environment"), "Production EU");
    await click(submit(host));
    expect(calls).toHaveLength(1);
    expect(host.textContent).toContain("Required.");
    expect(host.textContent).toContain("Use up to 32 lowercase letters");

    await type(byLabel(host, "Token name"), "One more");
    await type(byLabel(host, "Environment"), "");
    await click(submit(host));
    await waitFor(() => expect(host.textContent).toContain("This project has 50 active tokens."));
  });

  it("revokes a token only after the confirmation", async () => {
    const calls = stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }, { status: 204 }]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    await openMenu(host, "Actions for Production API");
    await click(menuItem("Revoke"));
    const dialog = [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
    expect(dialog?.hasAttribute("open")).toBe(true);
    expect(dialog?.textContent).toContain("Revoke the token “Production API”?");
    expect(dialog?.textContent).toContain("refuses this token within seconds");
    expect(calls).toHaveLength(1);
    await click(byRole(host, "button", "Revoke token"));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.method).toBe("DELETE");
    expect(calls[1]?.url).toBe(`/api/tenants/${f.TENANT_ID}/otlp-tokens/${f.TOKEN_ID}`);
    expect(calls[1]?.headers.get("X-Requested-With")).toBe("sa-console");
    await waitFor(() => expect(dialog?.hasAttribute("open")).toBe(false));
  });

  it("offers no form while the project is suspended or pending, and keeps revoke", async () => {
    stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }]);
    const host = await renderRoutes(routes({ ...base, status: "suspended" }), path);
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    expect(host.textContent).toContain("cannot be created while the project is suspended");
    expect(opener(host)).toBeNull();
    expect(host.querySelector('button[aria-label="Actions for Production API"]')).not.toBeNull();

    stubFetch([{ status: 200, body: page([]) }]);
    const pending = await renderRoutes(routes({ ...base, status: "pending" }), path);
    await waitFor(() => expect(pending.textContent).toContain("once operators have approved"));
  });

  it("offers no form until the current terms are accepted", async () => {
    stubFetch([{ status: 200, body: page([]) }]);
    const host = await renderRoutes(routes(base, false), path);
    await waitFor(() => expect(host.textContent).toContain("accepts the current terms"));
    expect(opener(host)).toBeNull();
  });

  it("shows one short exporter per stack, node first, and no example files", async () => {
    stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.querySelector("[role=radio]")).not.toBeNull());
    const tabs = [...host.querySelectorAll('[aria-label="Language"] [role=radio]')].map(
      (t) => t.textContent,
    );
    expect(tabs).toEqual(["Node.js", "Go", "Python", "Rust", "Java"]);
    const node = host.querySelector("[data-snippet=node] code")?.textContent ?? "";
    expect(node).toContain("new OTLPMetricExporter({");
    expect(node.split("\n").length).toBeLessThanOrEqual(5);
    // no file of the example and no path inside the repository
    expect(host.textContent).not.toContain("src/telemetry.js");
    expect(host.textContent).not.toContain("new NodeSDK(");
    expect(host.textContent).not.toContain("apps/console");
  });

  it("marks the steps by state: token done, exporter now, data awaited", async () => {
    stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    // the token step is a done row, the exporter the step to do now, and
    // the first export is awaited in it
    expect(host.textContent).toContain("2 of 3");
    expect(host.querySelector("h3")?.textContent).toContain("Add the exporter");
    expect(host.querySelector("[data-waiting]")?.textContent).toContain(
      "Waiting for the first export",
    );
  });
});

describe("validateToken", () => {
  it("accepts a name with an empty or label-shaped environment", () => {
    expect(validateToken({ name: "API", environment: "" })).toEqual({});
    expect(validateToken({ name: "API", environment: "staging-eu.1" })).toEqual({});
  });

  it("refuses an environment the contract refuses", () => {
    expect(validateToken({ name: "API", environment: "-x" }).environment).toBeDefined();
    expect(validateToken({ name: "API", environment: "a".repeat(33) }).environment).toBeDefined();
  });
});

describe("lastUsedToken", () => {
  it("picks the token used last, or none when no token was used", () => {
    const t = f.otlpTokenBody as never as Parameters<typeof lastUsedToken>[0][number];
    expect(lastUsedToken([t])).toBeNull();
    const a = { ...t, id: "a", last_seen_at: "2026-11-10T10:00:00Z" };
    const b = { ...t, id: "b", last_seen_at: "2026-11-12T10:00:00Z" };
    expect(lastUsedToken([a, t, b])?.id).toBe("b");
  });
});

// radix opens a menu on pointer down, not on click
async function openMenu(scope: HTMLElement, label: string) {
  const trigger = scope.querySelector<HTMLElement>(`button[aria-label="${label}"]`);
  if (!trigger) throw new Error(`no menu ${label}`);
  await act(async () => {
    trigger.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 }));
  });
}

function menuItem(text: string): HTMLElement {
  const el = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find((m) =>
    m.textContent?.includes(text),
  );
  if (!el) throw new Error(`no menu item ${text}`);
  return el;
}
