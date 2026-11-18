// a fetch double that answers by request path instead of by call order, for
// pages that ask several routes at once. it records each request; it only
// stands in for the network in tests.

import { vi } from "vitest";
import type { Call } from "./helpers";

type Answer = { status: number; body?: unknown };

// a route answers with one response every time, or with the next of a list
// (the last one repeats). the first route whose method and pattern match
// answers; an unmatched request fails the test.
export type Route = {
  method?: string;
  path: string | RegExp;
  answer: Answer | Answer[];
};

export function routeFetch(routes: Route[]) {
  const calls: Call[] = [];
  const used = new Map<Route, number>();
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
      const path = input.split("?")[0] ?? input;
      const route = routes.find(
        (r) =>
          (r.method ?? "GET") === method &&
          (typeof r.path === "string" ? r.path === path : r.path.test(input)),
      );
      if (!route) throw new Error(`unexpected request ${method} ${input}`);
      let answer: Answer;
      if (Array.isArray(route.answer)) {
        const i = used.get(route) ?? 0;
        used.set(route, i + 1);
        answer = route.answer[Math.min(i, route.answer.length - 1)] as Answer;
      } else answer = route.answer;
      const hasBody = answer.body !== undefined && answer.status !== 204;
      return new Response(hasBody ? JSON.stringify(answer.body) : null, {
        status: answer.status,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      });
    }),
  );
  return calls;
}
