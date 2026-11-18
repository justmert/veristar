// the one create form of the operator access keys: a machine api key
// (contract 9.2) or an ai assistant key (contract 12.8). both take a name
// and a lifetime; a machine key also takes what it may read.

import { ApiError } from "@/api/client";
import type { ApiKeyInput } from "@/api/endpoints";
import type { McpKeyInput } from "@/api/mcp";

export type KeyType = "machine" | "assistant";

export type KeyForm = {
  type: KeyType;
  name: string;
  scope: "all" | "tenant";
  tenantId: string;
  days: string;
};

export type KeyErrors = Partial<Record<"name" | "tenantId" | "days", string>>;

export const DEFAULT_EXPIRY_DAYS = 90;
export const MAX_EXPIRY_DAYS = 365;

export const EMPTY_KEY_FORM: KeyForm = {
  type: "machine",
  name: "",
  scope: "all",
  tenantId: "",
  days: String(DEFAULT_EXPIRY_DAYS),
};

// contract 3.2 refuses control characters in a machine key's name except
// newline and tab; 12.8 refuses all of them in an assistant key's name
function isControl(ch: string, type: KeyType): boolean {
  const c = ch.codePointAt(0) ?? 0;
  if (type === "machine" && (ch === "\n" || ch === "\t")) return false;
  return c < 0x20 || c === 0x7f;
}

export type KeyBody =
  | { type: "machine"; body: ApiKeyInput }
  | { type: "assistant"; body: McpKeyInput };

// the field rules, checked before sending so the form can say which field
// is wrong; the server checks them again
export function validateKeyForm(f: KeyForm): { errors: KeyErrors; body: KeyBody | null } {
  const errors: KeyErrors = {};
  const name = f.name.trim();
  if (!name) errors.name = "Enter a name.";
  else if ([...name].length > 100) errors.name = "Use at most 100 characters.";
  else if ([...name].some((ch) => isControl(ch, f.type)))
    errors.name = "Remove control characters.";
  if (f.type === "machine" && f.scope === "tenant" && !f.tenantId)
    errors.tenantId = "Choose a project.";
  const days = Number(f.days);
  if (!/^\d+$/.test(f.days.trim()) || days < 1 || days > MAX_EXPIRY_DAYS)
    errors.days = `Enter a whole number of days from 1 to ${MAX_EXPIRY_DAYS}.`;
  if (Object.keys(errors).length) return { errors, body: null };
  if (f.type === "assistant")
    return { errors, body: { type: "assistant", body: { name, expires_in_days: days } } };
  const body: ApiKeyInput = { name, scope: "metrics:read", expires_in_days: days };
  if (f.scope === "tenant") body.tenant_id = f.tenantId;
  return { errors, body: { type: "machine", body } };
}

// the 422 field codes of 9.2 and 12.8 on the form's fields
export function serverKeyErrors(err: unknown): KeyErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: KeyErrors = {};
  for (const [field, code] of Object.entries(err.fields)) {
    if (field === "name")
      out.name = code === "too_long" ? "Use at most 100 characters." : "Enter a valid name.";
    if (field === "tenant_id")
      out.tenantId =
        code === "unknown_tenant"
          ? "That project does not exist or was removed."
          : "Choose a project.";
    if (field === "expires_in_days") out.days = `Enter 1 to ${MAX_EXPIRY_DAYS} days.`;
  }
  return out;
}
