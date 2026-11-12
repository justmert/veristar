// a minimal render and event layer over react-dom and jsdom, so component
// tests need no testing library beyond the pinned toolchain

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, vi } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// jsdom has no layout and no ResizeObserver; radix measures controls with it.
// every browser the console supports has it, so this only fills the test dom.
if (!("ResizeObserver" in globalThis)) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

const mounted: { root: Root; host: HTMLElement }[] = [];

afterEach(() => {
  for (const m of mounted.splice(0)) {
    act(() => m.root.unmount());
    m.host.remove();
  }
  vi.unstubAllGlobals();
});

export async function render(ui: ReactNode, path = "/"): Promise<HTMLElement> {
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  mounted.push({ root, host });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const router = createMemoryRouter(
    [
      { path: "*", element: ui },
      { path: "/projects/:tenantId/sites", element: <p>sites page</p> },
    ],
    { initialEntries: [path] },
  );
  await act(async () => {
    root.render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  });
  return host;
}

export function byLabel(host: HTMLElement, text: string): HTMLInputElement | HTMLTextAreaElement {
  const label = [...host.querySelectorAll("label")].find((l) =>
    (l.textContent ?? "").trim().startsWith(text),
  );
  if (!label) throw new Error(`no label "${text}"`);
  const el = host.ownerDocument.getElementById(label.htmlFor);
  if (!el) throw new Error(`label "${text}" points at nothing`);
  return el as HTMLInputElement | HTMLTextAreaElement;
}

export function byRole(host: HTMLElement, role: string, name: string): HTMLElement {
  const all = [
    ...host.querySelectorAll<HTMLElement>(role === "button" ? "button" : `[role=${role}]`),
  ];
  const el = all.find((e) => (e.textContent ?? "").trim() === name);
  if (!el) throw new Error(`no ${role} "${name}"`);
  return el;
}

// sets a value the way a user does: react tracks the native value setter
export async function type(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement : HTMLInputElement;
  const setter = Object.getOwnPropertyDescriptor(proto.prototype, "value")?.set;
  await act(async () => {
    setter?.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

export async function click(el: HTMLElement) {
  await act(async () => {
    el.click();
  });
}

export async function waitFor(check: () => void, timeoutMs = 2000) {
  const start = Date.now();
  for (;;) {
    try {
      check();
      return;
    } catch (err) {
      if (Date.now() - start > timeoutMs) throw err;
      await act(async () => {
        await new Promise((r) => setTimeout(r, 10));
      });
    }
  }
}

export type Call = { url: string; method: string; headers: Headers; body: unknown };

// a fetch double answering from a list of recorded responses, recording each
// request; it only stands in for the network in tests
export function stubFetch(
  answers: { status: number; body?: unknown; headers?: Record<string, string> }[],
) {
  const calls: Call[] = [];
  const queue = [...answers];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init: RequestInit = {}) => {
      calls.push({
        url: input,
        method: init.method ?? "GET",
        headers: new Headers(init.headers),
        body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      });
      const next = queue.shift();
      if (!next) throw new Error(`unexpected request ${init.method ?? "GET"} ${input}`);
      const hasBody = next.body !== undefined && next.status !== 204;
      return new Response(hasBody ? JSON.stringify(next.body) : null, {
        status: next.status,
        headers: { "Content-Type": "application/json; charset=utf-8", ...next.headers },
      });
    }),
  );
  return calls;
}
