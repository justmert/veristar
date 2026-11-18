import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { validateKeyForm } from "@/lib/access-keys";
import { AccessKeysPage } from "@/pages/operator/keys";
import * as c from "./fixtures/contract";
import * as f from "./fixtures/global";
import { byLabel, byRole, type Call, click, renderRoutes, type, waitFor } from "./helpers";

const operatorRoutes = [{ path: "/operator/keys", element: <AccessKeysPage /> }];

// answers by method and path, for pages whose queries start together and
// may reach the network in either order. test double only.
function routeFetch(
  answer: (method: string, url: string, n: number) => { status: number; body?: unknown },
): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit = {}) => {
      const method = init.method ?? "GET";
      calls.push({
        url: input,
        method,
        headers: new Headers(init.headers),
        body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      });
      const a = answer(
        method,
        input,
        calls.filter((c) => c.method === method && c.url === input).length,
      );
      return new Response(a.body === undefined ? null : JSON.stringify(a.body), {
        status: a.status,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return calls;
}

describe("access keys", () => {
  it("validates the one create form with the rules of 9.2 and 12.8", () => {
    const base = {
      type: "machine" as const,
      name: "a",
      scope: "all" as const,
      tenantId: "",
      days: "90",
    };
    expect(validateKeyForm({ ...base, name: " " }).errors.name).toBeDefined();
    expect(validateKeyForm({ ...base, scope: "tenant" }).errors.tenantId).toBeDefined();
    expect(validateKeyForm({ ...base, days: "366" }).errors.days).toBeDefined();
    expect(validateKeyForm({ ...base, name: "a\u0007" }).errors.name).toBeDefined();
    // a tab is fine in a machine key's name, not in an assistant key's
    expect(validateKeyForm({ ...base, name: "a\tb" }).errors.name).toBeUndefined();
    expect(validateKeyForm({ ...base, type: "assistant", name: "a\tb" }).errors.name).toBeDefined();
    expect(
      validateKeyForm({
        ...base,
        name: " warehouse ",
        scope: "tenant",
        tenantId: c.TENANT_ID,
        days: "30",
      }).body,
    ).toEqual({
      type: "machine",
      body: {
        name: "warehouse",
        scope: "metrics:read",
        tenant_id: c.TENANT_ID,
        expires_in_days: 30,
      },
    });
    expect(validateKeyForm({ ...base, type: "assistant", name: "laptop" }).body).toEqual({
      type: "assistant",
      body: { name: "laptop", expires_in_days: 90 },
    });
  });

  it("creates a key, shows it once, and revokes it from the row menu after a confirmation", async () => {
    let revoked = false;
    let created = false;
    const calls = routeFetch((method, url) => {
      if (url.startsWith("/api/operator/tenants?"))
        return { status: 200, body: { items: [c.operatorSummaryBody], next_cursor: null } };
      if (url.startsWith("/api/operator/mcp-credentials"))
        return { status: 200, body: { items: [], next_cursor: null } };
      if (method === "POST") {
        created = true;
        return { status: 201, body: f.apiKeyCreatedBody };
      }
      if (method === "DELETE") {
        revoked = true;
        return { status: 204 };
      }
      if (url === "/api/operator/api-keys?limit=200")
        return {
          status: 200,
          body: revoked
            ? { items: [f.revokedKeyBody], next_cursor: null }
            : created
              ? f.apiKeyPageBody
              : { items: [], next_cursor: null },
        };
      return { status: 500 };
    });
    const host = await renderRoutes(operatorRoutes, "/operator/keys");
    await waitFor(() => expect(host.textContent).toContain("No key yet"));
    expect(host.querySelector("h1")?.textContent).toBe("Access keys");
    // the server address shows once
    expect(host.textContent?.split(`${window.location.origin}/mcp`).length).toBeGreaterThan(1);
    expect(
      calls.some((x) => x.url.includes("status=active%2Cpending%2Csuspended%2Crejected")),
    ).toBe(true);

    // the header button opens the form, as on the other operator pages
    expect(host.querySelector("form[aria-label='Create a key']")).toBeNull();
    await click(byRole(host, "button", "Create key"));
    await type(byLabel(host, "Name") as HTMLInputElement, "warehouse");
    await click(byRole(host, "button", "Create key"));
    // the one-time secret is masked until it is revealed, and the curl
    // example shows it masked too; only its copy holds the key
    await waitFor(() => byRole(host, "button", "Copy key"));
    expect(host.querySelector('[aria-label="Secret hidden"]')).toBeTruthy();
    expect(host.textContent).not.toContain(f.KEY_PLAIN);
    expect(host.textContent).toContain("Bearer sa_op_••••");
    await click(host.querySelector('[aria-label="Show the secret"]') as HTMLElement);
    await waitFor(() => expect(host.textContent).toContain(f.KEY_PLAIN));
    const post = calls.find((x) => x.method === "POST");
    expect(post).toMatchObject({
      method: "POST",
      url: "/api/operator/api-keys",
      body: { name: "warehouse", scope: "metrics:read", expires_in_days: 90 },
    });
    expect(post?.headers.get("X-Requested-With")).toBe("sa-console");

    // done stays disabled until the operator says the key is stored
    expect(byRole(host, "button", "Done").hasAttribute("disabled")).toBe(true);
    const stored = host.querySelector("section[aria-label='New secret'] [role=checkbox]");
    await click(stored as HTMLElement);
    await click(byRole(host, "button", "Done"));
    await waitFor(() => expect(host.querySelector(`[data-key-id="${f.KEY_ID}"]`)).not.toBeNull());
    expect(host.textContent).not.toContain(f.KEY_PLAIN);

    await openMenu(host.querySelector('button[aria-label="Actions for warehouse"]') as HTMLElement);
    await click(menuItem("Revoke"));
    const dialog = [...host.querySelectorAll("dialog")].find((d) => d.hasAttribute("open"));
    expect(dialog?.textContent).toContain("The next request with it is refused");
    const confirm = [...(dialog?.querySelectorAll("button") ?? [])].find(
      (b) => b.textContent === "Revoke",
    );
    if (!confirm) throw new Error("no confirm button");
    await click(confirm);
    await waitFor(() => expect(host.textContent).toContain("Revoked"));
    expect(calls.find((x) => x.method === "DELETE")?.url).toBe(
      `/api/operator/api-keys/${f.KEY_ID}`,
    );
  });

  it("shows usage per machine key from 9.11", async () => {
    const calls = routeFetch((_m, url) => {
      if (url.startsWith("/api/operator/tenants?"))
        return { status: 200, body: { items: [], next_cursor: null } };
      if (url.startsWith("/api/operator/mcp-credentials"))
        return { status: 200, body: { items: [], next_cursor: null } };
      if (url === "/api/operator/api-keys?limit=200")
        return { status: 200, body: f.apiKeyPageBody };
      if (url.endsWith("/usage")) return { status: 200, body: f.apiKeyUsageBody };
      return { status: 500 };
    });
    const host = await renderRoutes(operatorRoutes, "/operator/keys");
    await waitFor(() => expect(host.querySelector(`[data-key-id="${f.KEY_ID}"]`)).not.toBeNull());
    await openMenu(host.querySelector('button[aria-label="Actions for warehouse"]') as HTMLElement);
    await click(menuItem("Show usage"));
    await waitFor(() =>
      expect(host.querySelector("[data-usage=requests]")?.textContent).toBe("12"),
    );
    expect(host.querySelector("[data-usage=rejected]")?.textContent).toBe("2");
    expect(calls.some((x) => x.url === `/api/operator/api-keys/${f.KEY_ID}/usage`)).toBe(true);
    // the row menu opens again with the usage open
    await openMenu(host.querySelector('button[aria-label="Actions for warehouse"]') as HTMLElement);
    await waitFor(() => expect(menuItem("Revoke")).toBeTruthy());
  });
});

async function openMenu(trigger: HTMLElement) {
  await act(async () => {
    trigger.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 }));
  });
}

function menuItem(text: string): HTMLElement {
  const el = [...document.querySelectorAll<HTMLElement>("[role=menuitem]")].find(
    (m) => m.textContent === text,
  );
  if (!el) throw new Error(`no menu item ${text}`);
  return el;
}
