// client-side copy of the validation rules in contract 3.2. the control plane
// is the authority and answers 422 with field codes; checking here first only
// saves a round trip and points at the field before the request is sent.
// every function returns a field code of contract 2.5, or null when valid.

export type FieldCode =
  | "required"
  | "too_long"
  | "too_short"
  | "too_many"
  | "invalid_format"
  | "not_https_url"
  | "not_bare_origin"
  | "origin_outside_domain"
  | "invalid_domain"
  | "invalid_strkey"
  | "invalid_github_login"
  | "unknown_github_login"
  | "invalid_role"
  | "duplicate"
  | "unknown_tracker_version"
  | "invalid_value"
  | "unknown_scf_award";

// text fields refuse control characters other than newline and tab
// biome-ignore lint/suspicious/noControlCharactersInRegex: matching control characters is the point
const CONTROL = /[\u0000-\u0008\u000b-\u001f\u007f]/;

function text(v: string, min: number, max: number): FieldCode | null {
  if (CONTROL.test(v)) return "invalid_format";
  const len = [...v].length;
  if (len === 0 && min > 0) return "required";
  if (len < min) return "too_short";
  if (len > max) return "too_long";
  return null;
}

export const tenantName = (v: string) => text(v.trim(), 1, 100);
export const siteName = (v: string) => text(v.trim(), 1, 100);
export const description = (v: string) => text(v, 0, 1000);
export const stellarUsage = (v: string) => text(v.trim(), 1, 2000);
// 5.2 operator reasons: 1 to 1,000 characters after trimming
export const reason = (v: string, required: boolean) =>
  v.trim() === "" && !required ? null : text(v.trim(), 1, 1000);

const SLUG = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export function slug(v: string): FieldCode | null {
  if (v === "") return null;
  return SLUG.test(v) ? null : "invalid_format";
}

export function httpsUrl(v: string, required: boolean): FieldCode | null {
  if (v === "") return required ? "required" : null;
  if (v.length > 2048) return "too_long";
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return "not_https_url";
  }
  return u.protocol === "https:" && u.hostname !== "" ? null : "not_https_url";
}

const LOGIN = /^[A-Za-z0-9](?:[A-Za-z0-9]|-(?=[A-Za-z0-9])){0,38}$/;
export function githubLogin(v: string): FieldCode | null {
  if (v === "") return "required";
  return LOGIN.test(v) ? null : "invalid_github_login";
}

const LABEL = /^(?!-)[a-z0-9-]{1,63}(?<!-)$/;
export function domain(v: string): FieldCode | null {
  if (v === "") return "required";
  if (v.length > 253 || v.endsWith(".") || !v.includes(".")) return "invalid_domain";
  if (v !== v.toLowerCase()) return "invalid_domain";
  return v.split(".").every((l) => LABEL.test(l)) ? null : "invalid_domain";
}

const LOOPBACK = new Set(["localhost", "127.0.0.1"]);

// origins of one site: 1 to 20 unique bare origins under the site's domain,
// https only, except plain http on the two loopback names for local testing
export function origin(v: string, siteDomain: string): FieldCode | null {
  if (v === "") return "required";
  let u: URL;
  try {
    u = new URL(v);
  } catch {
    return "not_bare_origin";
  }
  if (u.origin !== v) return "not_bare_origin";
  if (u.protocol === "http:" && LOOPBACK.has(u.hostname)) return null;
  if (u.protocol !== "https:") return "not_bare_origin";
  if (siteDomain === "") return null;
  const host = u.hostname;
  if (host !== siteDomain && !host.endsWith(`.${siteDomain}`)) return "origin_outside_domain";
  return null;
}

export function originList(list: string[], siteDomain: string): Record<string, FieldCode> {
  const out: Record<string, FieldCode> = {};
  if (list.length === 0) out.origins = "required";
  if (list.length > 20) out.origins = "too_many";
  const seen = new Set<string>();
  list.forEach((o, i) => {
    const code = origin(o, siteDomain);
    if (code) out[`origins[${i}]`] = code;
    else if (seen.has(o)) out[`origins[${i}]`] = "duplicate";
    seen.add(o);
  });
  return out;
}

// strkey (sep-23): base32 of version byte, payload and a crc16-xmodem
// checksum, little endian. enrollment accepts G, M and C (contract 3.2).
const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
const STRKEY_TYPES: Record<string, { version: number; length: number }> = {
  G: { version: 6 << 3, length: 56 },
  C: { version: 2 << 3, length: 56 },
  M: { version: 12 << 3, length: 69 },
};

function base32Decode(s: string): Uint8Array | null {
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of s) {
    const idx = B32.indexOf(ch);
    if (idx < 0) return null;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  // leftover bits must be zero padding, else the encoding is not canonical
  if (bits > 0 && (value & ((1 << bits) - 1)) !== 0) return null;
  return Uint8Array.from(out);
}

function crc16xmodem(data: Uint8Array): number {
  let crc = 0;
  for (const byte of data) {
    crc ^= byte << 8;
    for (let i = 0; i < 8; i++) {
      crc = crc & 0x8000 ? ((crc << 1) ^ 0x1021) & 0xffff : (crc << 1) & 0xffff;
    }
  }
  return crc;
}

export function strkey(v: string): FieldCode | null {
  const t = STRKEY_TYPES[v.charAt(0)];
  if (!t || v.length !== t.length) return "invalid_strkey";
  const raw = base32Decode(v);
  if (!raw || raw.length < 3 || raw[0] !== t.version) return "invalid_strkey";
  const body = raw.subarray(0, raw.length - 2);
  const sum = (raw[raw.length - 2] ?? 0) | ((raw[raw.length - 1] ?? 0) << 8);
  return crc16xmodem(body) === sum ? null : "invalid_strkey";
}

// people-facing text for the field codes of contract 2.5
export function fieldMessage(code: string): string {
  switch (code) {
    case "required":
      return "Required.";
    case "too_long":
      return "Too long.";
    case "too_short":
      return "Too short.";
    case "too_many":
      return "Too many entries.";
    case "invalid_format":
      return "Not in the expected format.";
    case "not_https_url":
      return "Enter a full https:// address.";
    case "not_bare_origin":
      return "Enter only scheme and host, like https://example.org, with no path.";
    case "origin_outside_domain":
      return "The host must be the site's domain or one of its subdomains.";
    case "invalid_domain":
      return "Enter a lowercase domain like example.org, without https:// or a path.";
    case "invalid_strkey":
      return "Not a valid account (G...), muxed account (M...) or contract (C...) id.";
    case "invalid_github_login":
      return "Not a valid GitHub username.";
    case "unknown_github_login":
      return "No GitHub user has this username.";
    case "invalid_role":
      return "Choose owner or member.";
    case "duplicate":
      return "Listed twice.";
    case "unknown_tracker_version":
      return "This tracker version does not exist.";
    case "invalid_value":
      return "Not one of the allowed values.";
    case "unknown_scf_award":
      return "This SCF award is not in the directory now. Search again and pick the award.";
    default:
      return `Rejected by the server (${code}).`;
  }
}
