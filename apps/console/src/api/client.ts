// one fetch path for every control plane call. it adds the csrf header the
// control plane requires on state changes (contract 1.2), parses every body
// with its zod schema and turns the error envelope (contract 2.2) into an
// ApiError, so pages branch on stable codes and never on message text.

import type { z } from "zod";
import { errorEnvelope } from "./schemas";

export const API_BASE = "/api";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: Record<string, string>;
  readonly requestId: string | null;
  readonly retryAfter: number | null;

  constructor(opts: {
    status: number;
    code: string;
    message: string;
    fields?: Record<string, string>;
    requestId?: string | null;
    retryAfter?: number | null;
  }) {
    super(opts.message);
    this.name = "ApiError";
    this.status = opts.status;
    this.code = opts.code;
    this.fields = opts.fields ?? {};
    this.requestId = opts.requestId ?? null;
    this.retryAfter = opts.retryAfter ?? null;
  }
}

// a 2xx body that does not match the contract. shown as an error instead of
// rendering half-parsed data.
export class ResponseShapeError extends Error {
  readonly path: string;
  constructor(path: string, detail: string) {
    super(`the response of ${path} does not match the api contract: ${detail}`);
    this.name = "ResponseShapeError";
    this.path = path;
  }
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

const MUTATING: ReadonlySet<Method> = new Set(["POST", "PUT", "PATCH", "DELETE"]);

function retryAfterSeconds(res: Response): number | null {
  const v = res.headers.get("Retry-After");
  if (v === null) return null;
  const n = Number(v);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

async function errorFrom(res: Response, path: string): Promise<ApiError> {
  let body: unknown = null;
  const text = await res.text();
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = null;
  }
  const parsed = errorEnvelope.safeParse(body);
  if (parsed.success) {
    const e = parsed.data.error;
    return new ApiError({
      status: res.status,
      code: e.code,
      message: e.message,
      fields: e.fields,
      requestId: e.request_id,
      retryAfter: retryAfterSeconds(res),
    });
  }
  // a proxy in front of the control plane (caddy, a load balancer) can answer
  // without the envelope; the status is still reported as it is
  return new ApiError({
    status: res.status,
    code: res.status === 502 || res.status === 504 ? "unavailable" : "unexpected_response",
    message: `${path} answered ${res.status} without an error body`,
    requestId: res.headers.get("X-Request-Id"),
    retryAfter: retryAfterSeconds(res),
  });
}

export async function request<S extends z.ZodType>(
  method: Method,
  path: string,
  schema: S,
  body?: unknown,
  signal?: AbortSignal,
): Promise<z.infer<S>> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (MUTATING.has(method)) headers["X-Requested-With"] = "sa-console";
  if (body !== undefined) headers["Content-Type"] = "application/json; charset=utf-8";

  const res = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
    signal,
  });
  if (!res.ok) throw await errorFrom(res, path);

  let json: unknown;
  try {
    json = await res.json();
  } catch {
    throw new ResponseShapeError(path, `${res.status} body is not json`);
  }
  const parsed = schema.safeParse(json);
  if (!parsed.success) {
    const first = parsed.error.issues[0];
    const where = first ? first.path.join(".") || "(root)" : "(root)";
    throw new ResponseShapeError(path, `${where}: ${first?.message ?? "invalid"}`);
  }
  return parsed.data;
}

// for routes that answer 204 without a body (delete, logout)
export async function requestNoContent(method: Method, path: string): Promise<void> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (MUTATING.has(method)) headers["X-Requested-With"] = "sa-console";
  const res = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    credentials: "same-origin",
    cache: "no-store",
    redirect: "error",
  });
  if (!res.ok) throw await errorFrom(res, path);
  if (res.status !== 204) throw new ResponseShapeError(path, `expected 204, got ${res.status}`);
}
