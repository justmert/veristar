import { Outlet } from "react-router";
import { describe, expect, it } from "vitest";
import type { Tenant } from "@/api/schemas";
import { ConnectApiPage, validateToken } from "@/pages/project/connect-api";
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

describe("api analytics setup page (contract section 6)", () => {
  it("lists the active tokens with prefix, environment and use", async () => {
    const calls = stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    expect(calls[0]?.url).toBe(listPath);
    expect(host.textContent).toContain("sa_otlp_Ab3x...");
    expect(host.textContent).toContain("production");
    expect(host.textContent).toContain("not used yet");
    // the plaintext never comes from a list
    expect(host.textContent).not.toContain(f.OTLP_PLAIN);
  });

  it("asks for revoked tokens only when they are to be shown", async () => {
    const revoked = { ...f.otlpTokenBody, status: "revoked", revoked_at: "2026-11-12T10:00:00Z" };
    const calls = stubFetch([
      { status: 200, body: page([]) },
      { status: 200, body: page([revoked]) },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No active tokens."));
    // the checkbox sits inside its label
    const toggle = [...host.querySelectorAll("label")]
      .find((l) => l.textContent?.trim() === "Show revoked tokens")
      ?.querySelector("input");
    if (!toggle) throw new Error("no show revoked checkbox");
    await click(toggle);
    await waitFor(() => expect(host.textContent).toContain("Revoked"));
    expect(calls[1]?.url).toBe(
      `/api/tenants/${f.TENANT_ID}/otlp-tokens?include_revoked=true&limit=200`,
    );
    // a revoked token cannot be revoked again
    expect(() => byRole(host, "button", "Revoke")).toThrow();
  });

  it("creates a token and shows the plaintext once, until it is dismissed", async () => {
    const calls = stubFetch([
      { status: 200, body: page([]) },
      { status: 201, body: f.otlpTokenCreatedBody },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No active tokens."));
    await type(byLabel(host, "Token name"), "  Production API ");
    await type(byLabel(host, "Environment"), "production");
    await click(byRole(host, "button", "Create token"));
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
    expect(host.textContent).toContain("It is shown only this once");
    expect(byLabel(host, "Token name").value).toBe("");
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
    await waitFor(() => expect(host.textContent).toContain("No active tokens."));
    await type(byLabel(host, "Token name"), "Staging");
    await click(byRole(host, "button", "Create token"));
    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]?.body).toEqual({ name: "Staging" });
  });

  it("checks the fields before sending and shows the server's field codes", async () => {
    const calls = stubFetch([
      { status: 200, body: page([]) },
      { status: 422, body: f.errorBody("validation_failed", { name: "too_many" }) },
    ]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No active tokens."));
    await type(byLabel(host, "Environment"), "Production EU");
    await click(byRole(host, "button", "Create token"));
    expect(calls).toHaveLength(1);
    expect(host.textContent).toContain("Required.");
    expect(host.textContent).toContain("Use up to 32 lowercase letters");

    await type(byLabel(host, "Token name"), "One more");
    await type(byLabel(host, "Environment"), "");
    await click(byRole(host, "button", "Create token"));
    await waitFor(() => expect(host.textContent).toContain("This project has 50 active tokens."));
  });

  it("revokes a token only after the confirmation", async () => {
    const calls = stubFetch([{ status: 200, body: page([f.otlpTokenBody]) }, { status: 204 }]);
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("Production API"));
    await click(byRole(host, "button", "Revoke"));
    const dialog = host.querySelector("dialog");
    expect(dialog?.hasAttribute("open")).toBe(true);
    expect(dialog?.textContent).toContain("Revoke Production API?");
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
    expect(() => byLabel(host, "Token name")).toThrow();
    expect(byRole(host, "button", "Revoke")).toBeTruthy();

    stubFetch([{ status: 200, body: page([]) }]);
    const pending = await renderRoutes(routes({ ...base, status: "pending" }), path);
    await waitFor(() => expect(pending.textContent).toContain("once operators have approved"));
  });

  it("offers no form until the current terms are accepted", async () => {
    stubFetch([{ status: 200, body: page([]) }]);
    const host = await renderRoutes(routes(base, false), path);
    await waitFor(() => expect(host.textContent).toContain("accepts the current terms"));
    expect(() => byLabel(host, "Token name")).toThrow();
  });

  it("says what the service keeps and that the tenant backend keeps everything", async () => {
    stubFetch([{ status: 200, body: page([]) }]);
    const host = await renderRoutes(routes(), path);
    const text = host.textContent ?? "";
    expect(text).toContain("Only HTTP server request duration is kept");
    expect(text).toContain("Traces are not stored");
    expect(text).toContain("Logs are accepted");
    expect(text).toContain("Your own backend keeps the full data");
  });

  it("shows the four language guides as tabs, node first", async () => {
    stubFetch([{ status: 200, body: page([]) }]);
    const host = await renderRoutes(routes(), path);
    const tabs = [...host.querySelectorAll("[role=tab]")].map((t) => t.textContent);
    expect(tabs).toEqual(["Node.js", "Go", "Python", "Rust"]);
    expect(host.textContent).toContain("src/telemetry.js");
    expect(host.textContent).toContain("new NodeSDK(");
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
