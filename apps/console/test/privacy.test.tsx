import { Outlet } from "react-router";
import { describe, expect, it, vi } from "vitest";
import { erasureRequest, erasureRequestPage, type Tenant } from "@/api/schemas";
import { ipField, isIpAddress, sessionField, userAgentField } from "@/lib/erasure";
import { PrivacyPage } from "@/pages/project/privacy";
import * as f from "./fixtures/contract";
import { byLabel, byRole, type Call, click, renderRoutes, type, waitFor } from "./helpers";

const path = `/projects/${f.TENANT_ID}/privacy`;
const sitesUrl = `/api/tenants/${f.TENANT_ID}/sites?limit=200`;
const listUrl = `/api/tenants/${f.TENANT_ID}/erasure?limit=50`;
const postUrl = `/api/tenants/${f.TENANT_ID}/erasure`;
const oneUrl = `/api/tenants/${f.TENANT_ID}/erasure/${f.ERASURE_ID}`;

const IP = "203.0.113.77";
const UA = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0";

function routes(isOwner = true) {
  const ctx = {
    tenant: f.tenantBody as Tenant,
    me: f.meBody,
    isOwner,
    termsCurrent: true,
    readOnly: false,
  };
  return [
    {
      path: "/projects/:tenantId",
      element: <Outlet context={ctx} />,
      children: [{ path: "privacy", element: <PrivacyPage /> }],
    },
  ];
}

type Answer = { status: number; body?: unknown };

// the page reads the history, the request it sent and the site list in an
// order react query decides, so answers are looked up by method and url
// instead of taken from a queue. each key's answers are used in turn and the
// last one repeats (a poll that has finished keeps answering the same).
function serve(given: Record<string, Answer[]>): Call[] {
  // the history names who asked from the member list
  const table: Record<string, Answer[]> = {
    [`GET /api/tenants/${f.TENANT_ID}/members`]: [{ status: 200, body: f.membersBody }],
    ...given,
  };
  const calls: Call[] = [];
  const used: Record<string, number> = {};
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
      const key = `${method} ${input}`;
      const answers = table[key];
      if (!answers || answers.length === 0) throw new Error(`unexpected request ${key}`);
      const i = Math.min(used[key] ?? 0, answers.length - 1);
      used[key] = (used[key] ?? 0) + 1;
      const a = answers[i] as Answer;
      return new Response(a.body === undefined ? null : JSON.stringify(a.body), {
        status: a.status,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return calls;
}

const listOf = (...items: unknown[]) => ({ items, next_cursor: null });

// a choice of the form, found by the words it starts with
function radio(host: HTMLElement, text: string): HTMLElement {
  const el = [...host.querySelectorAll<HTMLElement>("[role=radio]")].find((r) =>
    (r.textContent ?? "").trim().startsWith(text),
  );
  if (!el) throw new Error(`no choice "${text}"`);
  return el;
}

function openDialog(): HTMLElement {
  const d = document.body.querySelector<HTMLElement>("dialog[open]");
  if (!d) throw new Error("no open dialog");
  return d;
}

// every value the browser keeps for this origin, to prove the typed address
// and user agent never land there
function storedText(): string {
  const all: string[] = [document.cookie];
  for (const s of [localStorage, sessionStorage]) {
    for (let i = 0; i < s.length; i++) {
      const k = s.key(i) ?? "";
      all.push(k, s.getItem(k) ?? "");
    }
  }
  return all.join("\n");
}

describe("erasure field checks (contract 10.2)", () => {
  it("accepts the addresses go's netip.ParseAddr accepts and nothing else", () => {
    for (const ok of [
      "203.0.113.7",
      "0.0.0.0",
      "255.255.255.255",
      "2001:db8::1",
      "::",
      "::1",
      "1::",
      "1:2:3:4:5:6:7:8",
      "1:2:3:4:5:6:7::",
      "::ffff:192.0.2.1",
      "64:ff9b::192.0.2.1",
      "1:2:3:4:5:6:192.0.2.1",
      "2001:DB8::A",
    ])
      expect(isIpAddress(ok), ok).toBe(true);
    for (const bad of [
      "",
      "203.0.113",
      "203.0.113.256",
      "203.0.113.07",
      "1.2.3.4.5",
      "fe80::1%eth0",
      "1:2:3:4:5:6:7:8:9",
      "1:2:3:4:5:6:7:8::",
      "1::2::3",
      ":1",
      "1:::2",
      "12345::",
      "::1.2.3",
      "1:2:3:4:5:6:7:1.2.3.4",
      "example.org",
    ])
      expect(isIpAddress(bad), bad).toBe(false);
    expect(ipField("  203.0.113.7 ")).toBeNull();
    expect(ipField(" ")).toBe("required");
  });

  it("checks the user agent and the session id", () => {
    expect(userAgentField(UA)).toBeNull();
    expect(userAgentField("")).toBe("required");
    expect(userAgentField(`Mozilla\t5.0`)).toBe("invalid_format");
    expect(userAgentField(`Mozilla\u00855.0`)).toBe("invalid_format");
    expect(userAgentField("a".repeat(2048))).toBeNull();
    // 1,025 two-byte characters are 2,050 bytes
    expect(userAgentField("é".repeat(1025))).toBe("too_long");
    expect(sessionField(f.SESSION_ID.toUpperCase())).toBeNull();
    expect(sessionField("not-a-uuid")).toBe("invalid_format");
  });
});

describe("erasure schemas (contract 10.1)", () => {
  it("parses the contract's request object and list envelope", () => {
    expect(erasureRequest.parse(f.erasureDoneBody)).toMatchObject({ kind: "visitor_today" });
    expect(erasureRequest.parse(f.erasureRunningBody).status).toBe("running");
    expect(erasureRequestPage.parse(listOf(f.erasureDoneBody)).items).toHaveLength(1);
  });

  it("refuses a response outside the contract", () => {
    expect(erasureRequest.safeParse({ ...f.erasureDoneBody, kind: "everything" }).success).toBe(
      false,
    );
    expect(erasureRequest.safeParse({ ...f.erasureDoneBody, outcome: "maybe" }).success).toBe(
      false,
    );
    const { message: _m, ...noMessage } = f.erasureDoneBody;
    expect(erasureRequest.safeParse(noMessage).success).toBe(false);
    expect(erasureRequest.safeParse({ ...f.erasureDoneBody, events_deleted: -1 }).success).toBe(
      false,
    );
    expect(erasureRequest.safeParse({ ...f.erasureDoneBody, day: "12.11.2026" }).success).toBe(
      false,
    );
  });
});

describe("privacy page", () => {
  it("shows the history to a member, without the form", async () => {
    serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [{ status: 200, body: listOf(f.erasureDoneBody) }],
    });
    const host = await renderRoutes(routes(false), path);
    await waitFor(() => expect(host.textContent).toContain("Deleted 2 session(s) with 4 event"));
    expect(host.textContent).toContain("One visitor's visits today");
    expect(host.textContent).toContain("Done");
    expect(host.textContent).toContain("Daily aggregates are being recomputed.");
    expect(host.textContent).toContain("Only owners of this project can request erasure.");
    expect(host.querySelector("form")).toBeNull();
    // what erasure can reach is in the docs, not in a side column
    expect(host.querySelector("aside")).toBeNull();
  });

  it("sends a visitor erasure once, clears the typed values and stores them nowhere", async () => {
    localStorage.clear();
    sessionStorage.clear();
    const calls = serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [
        { status: 200, body: listOf() },
        { status: 200, body: listOf(f.erasureDoneBody) },
      ],
      [`POST ${postUrl}`]: [{ status: 202, body: f.erasureRunningBody }],
      [`GET ${oneUrl}`]: [
        { status: 200, body: f.erasureRunningBody },
        { status: 200, body: f.erasureDoneBody },
      ],
    });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No erasure requested yet"));
    const ip = byLabel(host, "IP address");
    const ua = byLabel(host, "User agent");
    // the browser must not offer these values to anyone later
    expect(ip.getAttribute("autocomplete")).toBe("off");
    expect(ua.getAttribute("autocomplete")).toBe("off");
    expect(host.querySelector("form")?.getAttribute("autocomplete")).toBe("off");
    await type(ip, ` ${IP} `);
    await type(ua, `${UA} `);
    await click(byRole(host, "button", "Review and erase"));
    // nothing is sent before the confirmation
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(openDialog().textContent).toContain("Erase one visitor on example.org?");
    await click(byRole(openDialog(), "button", "Erase data"));

    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = calls.filter((c) => c.method === "POST");
    expect(post).toHaveLength(1);
    expect(post[0]?.url).toBe(postUrl);
    expect(post[0]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(post[0]?.body).toEqual({
      kind: "visitor_today",
      site_id: f.SITE_ID,
      ip: IP,
      user_agent: UA,
    });
    // cleared as soon as the request was answered
    await waitFor(() => expect(byLabel(host, "IP address").value).toBe(""));
    expect(byLabel(host, "User agent").value).toBe("");

    // the request is followed until it finishes, and its message is shown
    await waitFor(
      () => expect(host.textContent).toContain("One visitor's visits today on example.org: erased"),
      6000,
    );
    // the address and user agent were sent in the post body only
    for (const c of calls) {
      if (c.method === "POST") continue;
      expect(c.url).not.toContain(IP);
      expect(c.url).not.toContain(encodeURIComponent(UA));
    }
    expect(storedText()).not.toContain(IP);
    expect(storedText()).not.toContain("Firefox/140.0");
    expect(host.innerHTML).not.toContain(IP);
  });

  it("points at the field before sending and maps the server's 422 codes", async () => {
    const calls = serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [{ status: 200, body: listOf() }],
      [`POST ${postUrl}`]: [
        { status: 422, body: f.errorBody("validation_failed", { user_agent: "too_long" }) },
      ],
    });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No erasure requested yet"));
    await type(byLabel(host, "IP address"), "203.0.113.300");
    await click(byRole(host, "button", "Review and erase"));
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    expect(host.textContent).toContain("Not in the expected format.");
    expect(host.textContent).toContain("Required.");

    await type(byLabel(host, "IP address"), "2001:db8::7");
    await type(byLabel(host, "User agent"), UA);
    await click(byRole(host, "button", "Review and erase"));
    await click(byRole(openDialog(), "button", "Erase data"));
    await waitFor(() => expect(host.textContent).toContain("Too long."));
    expect(document.body.querySelector("dialog[open]")).toBeNull();
    // a 422 leaves the values in place so the owner can correct them
    expect(byLabel(host, "IP address").value).toBe("2001:db8::7");
  });

  it("erases one session by its id", async () => {
    const sessionBody = {
      ...f.erasureRunningBody,
      kind: "session",
      day: null,
      session_id: f.SESSION_ID,
    };
    const calls = serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [{ status: 200, body: listOf() }],
      [`POST ${postUrl}`]: [{ status: 202, body: sessionBody }],
      [`GET ${oneUrl}`]: [{ status: 200, body: sessionBody }],
    });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No erasure requested yet"));
    await click(radio(host, "One session"));
    await type(byLabel(host, "Session id"), ` ${f.SESSION_ID.toUpperCase()} `);
    await click(byRole(host, "button", "Review and erase"));
    await click(byRole(openDialog(), "button", "Erase data"));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      kind: "session",
      site_id: f.SITE_ID,
      session_id: f.SESSION_ID,
    });
    await waitFor(() => expect(host.textContent).toContain("One session on example.org: running"));
  });

  it("resets a site only after the domain is typed and the dialog is confirmed", async () => {
    const resetBody = { ...f.erasureRunningBody, kind: "site_reset", day: null };
    const calls = serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [{ status: 200, body: listOf() }],
      [`POST ${postUrl}`]: [
        { status: 409, body: f.errorBody("erasure_in_progress") },
        { status: 202, body: resetBody },
      ],
      [`GET ${oneUrl}`]: [{ status: 200, body: resetBody }],
    });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No erasure requested yet"));
    await click(radio(host, "Everything of this site"));
    await click(byRole(host, "button", "Review and erase"));
    const dialog = openDialog();
    expect(dialog.textContent).toContain("Reset all web data of example.org?");
    const confirm = byRole(dialog, "button", "Reset site data") as HTMLButtonElement;
    // the domain is typed in the dialog, exactly
    await type(byLabel(dialog, "Type example.org to confirm"), "example.com");
    expect(confirm.disabled).toBe(true);
    await type(byLabel(dialog, "Type example.org to confirm"), "example.org");
    expect(confirm.disabled).toBe(false);
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);

    // the first attempt meets a running request of the same site
    await click(confirm);
    await waitFor(() =>
      expect(dialog.textContent).toContain(
        "Another erasure request for this site is still running.",
      ),
    );
    await click(confirm);
    await waitFor(() =>
      expect(host.textContent).toContain("Everything of this site on example.org: running"),
    );
    const posts = calls.filter((c) => c.method === "POST");
    expect(posts).toHaveLength(2);
    expect(posts[1]?.body).toEqual({
      kind: "site_reset",
      site_id: f.SITE_ID,
      confirm_domain: "example.org",
    });
    expect(host.ownerDocument.querySelector("dialog[open]")).toBeNull();
  });

  it("says why nothing was erased when no gateway answers", async () => {
    serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [{ status: 200, body: listOf() }],
      [`POST ${postUrl}`]: [{ status: 503, body: f.errorBody("unavailable") }],
    });
    const host = await renderRoutes(routes(), path);
    await waitFor(() => expect(host.textContent).toContain("No erasure requested yet"));
    await type(byLabel(host, "IP address"), IP);
    await type(byLabel(host, "User agent"), UA);
    await click(byRole(host, "button", "Review and erase"));
    await click(byRole(openDialog(), "button", "Erase data"));
    await waitFor(() =>
      expect(host.textContent).toContain(
        "The ingestion service does not answer, so nothing was erased.",
      ),
    );
  });

  it("refuses a history answer outside the contract instead of showing it", async () => {
    serve({
      [`GET ${sitesUrl}`]: [{ status: 200, body: f.sitePageBody }],
      [`GET ${listUrl}`]: [
        { status: 200, body: listOf({ ...f.erasureDoneBody, status: "unknown" }) },
      ],
    });
    const host = await renderRoutes(routes(), path);
    await waitFor(() =>
      expect(host.textContent).toContain(
        "The server answered in a form this console does not read",
      ),
    );
  });
});
