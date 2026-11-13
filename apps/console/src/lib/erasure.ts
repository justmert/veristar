// client-side checks and wording for the erasure form (contract 10.2). the
// control plane is the authority and answers 422 with field codes; checking
// here first only points at the field before anything is sent.

import type { ErasureKind, ErasureRequest } from "@/api/schemas";
import type { FieldCode } from "@/lib/validate";

// contract 10.2: the user agent is at most 2,048 bytes
export const MAX_USER_AGENT_BYTES = 2048;

// biome-ignore lint/suspicious/noControlCharactersInRegex: matching control characters is the point
const CONTROL = /[\u0000-\u001f\u007f-\u009f]/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// dotted decimal as go's netip.ParseAddr reads it: four fields 0 to 255 and
// no leading zero, because 010 is octal to some parsers and decimal to others
function ipv4(s: string): boolean {
  const parts = s.split(".");
  if (parts.length !== 4) return false;
  return parts.every((p) => /^(0|[1-9][0-9]{0,2})$/.test(p) && Number(p) <= 255);
}

// rfc 4291 text form: eight groups of 1 to 4 hex digits, one :: for a run of
// zero groups, and an optional dotted ipv4 tail worth two groups. a zone
// (%eth0) is refused, as the contract does.
function ipv6(s: string): boolean {
  if (s.includes("%")) return false;
  let groups = 0;
  let body = s;
  const lastColon = s.lastIndexOf(":");
  if (lastColon >= 0 && s.slice(lastColon + 1).includes(".")) {
    if (!ipv4(s.slice(lastColon + 1))) return false;
    groups += 2;
    body = s.slice(0, lastColon + 1);
    // "::1.2.3.4" leaves "::", "a::1.2.3.4" leaves "a::"; a single trailing
    // colon after a group stands for the separator before the ipv4 tail
    if (body.endsWith(":") && !body.endsWith("::")) body = body.slice(0, -1);
  }
  const halves = body.split("::");
  if (halves.length > 2) return false;
  const count = (h: string): number | null => {
    if (h === "") return 0;
    const gs = h.split(":");
    return gs.every((g) => /^[0-9a-fA-F]{1,4}$/.test(g)) ? gs.length : null;
  };
  const counts = halves.map(count);
  if (counts.some((c) => c === null)) return false;
  groups += counts.reduce<number>((a, c) => a + (c ?? 0), 0);
  // :: stands for at least one zero group
  return halves.length === 2 ? groups <= 7 : groups === 8;
}

export function isIpAddress(v: string): boolean {
  return v.includes(":") ? ipv6(v) : ipv4(v);
}

export function ipField(v: string): FieldCode | null {
  const t = v.trim();
  if (t === "") return "required";
  return isIpAddress(t) ? null : "invalid_format";
}

export function userAgentField(v: string): FieldCode | null {
  const t = v.trim();
  if (t === "") return "required";
  if (new TextEncoder().encode(t).length > MAX_USER_AGENT_BYTES) return "too_long";
  return CONTROL.test(t) ? "invalid_format" : null;
}

// umami session ids are lowercase uuids; a pasted id is lowercased first,
// since the contract compares the canonical form
export function sessionField(v: string): FieldCode | null {
  const t = v.trim().toLowerCase();
  if (t === "") return "required";
  return UUID.test(t) ? null : "invalid_format";
}

export function confirmDomainField(v: string, domain: string): FieldCode | null {
  if (v.trim() === "") return "required";
  return v.trim() === domain ? null : "invalid_value";
}

export const KIND_LABEL: Record<ErasureKind, string> = {
  visitor_today: "One visitor, today",
  session: "One session",
  site_reset: "Whole site",
};

// one line for the history: what the request reached, in the words the
// control plane chose (message) when it has finished
export function requestSummary(r: ErasureRequest): string {
  if (r.status === "running" || r.status === "pending") return "Running";
  if (r.status === "failed") return "Failed";
  return r.outcome === "nothing_found" ? "Nothing found" : "Erased";
}

// the utc day the gateways use; a visitor_today request covers only it
export function utcToday(now: Date): string {
  return now.toISOString().slice(0, 10);
}
