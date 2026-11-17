// the operator step-up, passkeys and roster (contract 5.15 and 5.16): their
// schemas, calls and queries in one file

import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { request, requestNoContent } from "./client";
import type { PasskeyAnswer } from "./endpoints";
import { passkeyChallenge } from "./schemas";

const time = z.iso.datetime();
const enc = encodeURIComponent;

export const stepUpStatus = z.object({
  stepped_up: z.boolean(),
  stepped_up_at: time.nullable(),
  expires_at: time.nullable(),
  max_age_seconds: z.number().int().positive(),
  passkeys: z.number().int().nonnegative(),
  sign_in_passkey: z.boolean(),
  enrollment: z.enum(["open", "sign_in_required", "closed"]),
  enrollment_until: time.nullable(),
});
export type StepUpStatus = z.infer<typeof stepUpStatus>;

export const passkey = z.object({
  id: z.uuid(),
  name: z.string(),
  created_at: time,
  last_used_at: time.nullable(),
});
export type Passkey = z.infer<typeof passkey>;

export const rosterRole = z.enum(["operator", "operator_admin"]);
export type RosterRole = z.infer<typeof rosterRole>;

export const rosterEntry = z.object({
  id: z.uuid(),
  kind: z.enum(["github", "stellar", "passkey"]),
  github_id: z.number().int().positive().nullable(),
  github_login: z.string().nullable(),
  stellar_address: z.string().nullable(),
  role: rosterRole,
  status: z.enum(["active", "suspended"]),
  status_reason: z.string().nullable(),
  bootstrap: z.boolean(),
  enrollment_until: time.nullable(),
  created_at: time,
  updated_at: time,
  account: z
    .object({
      user_id: z.uuid(),
      display_name: z.string(),
      last_login_at: time.nullable(),
      passkeys: z.number().int().nonnegative(),
    })
    .nullable(),
});
export type RosterEntry = z.infer<typeof rosterEntry>;

export type RosterAction = "suspend" | "resume" | "remove" | "reset-passkeys";

export const operatorsApi = {
  stepUpStatus: (signal?: AbortSignal) =>
    request("GET", "/operator/step-up", stepUpStatus, undefined, signal),
  stepUpChallenge: () => request("POST", "/operator/step-up/challenge", passkeyChallenge, {}),
  stepUp: (answer: PasskeyAnswer) => request("POST", "/operator/step-up", stepUpStatus, answer),
  passkeys: (signal?: AbortSignal) =>
    request(
      "GET",
      "/operator/passkeys",
      z.object({ items: z.array(passkey), sign_in_passkey: z.boolean() }),
      undefined,
      signal,
    ),
  passkeyChallenge: () => request("POST", "/operator/passkeys/challenge", passkeyChallenge, {}),
  addPasskey: (name: string, answer: PasskeyAnswer) =>
    request("POST", "/operator/passkeys", passkey, { name, ...answer }),
  deletePasskey: (id: string) => requestNoContent("DELETE", `/operator/passkeys/${enc(id)}`),
  roster: (signal?: AbortSignal) =>
    request(
      "GET",
      "/operator/roster",
      z.object({ items: z.array(rosterEntry) }),
      undefined,
      signal,
    ),
  addOperator: (body: {
    github_login?: string;
    stellar_address?: string;
    role: RosterRole;
    reason: string;
  }) => request("POST", "/operator/roster", rosterEntry, body),
  rosterAction: (id: string, action: RosterAction, reason: string) =>
    action === "remove"
      ? requestNoContent("POST", `/operator/roster/${enc(id)}/remove`, { reason })
      : request("POST", `/operator/roster/${enc(id)}/${action}`, rosterEntry, { reason }),
  setRole: (id: string, role: RosterRole, reason: string) =>
    request("POST", `/operator/roster/${enc(id)}/role`, rosterEntry, { role, reason }),
};

export const operatorKeys = {
  stepUp: ["step-up"] as const,
  passkeys: ["operator", "passkeys"] as const,
  roster: ["operator", "roster"] as const,
};

export const stepUpQuery = queryOptions({
  queryKey: operatorKeys.stepUp,
  queryFn: ({ signal }) => operatorsApi.stepUpStatus(signal),
  staleTime: 30_000,
});

export const passkeysQuery = queryOptions({
  queryKey: operatorKeys.passkeys,
  queryFn: ({ signal }) => operatorsApi.passkeys(signal),
});

export const rosterQuery = queryOptions({
  queryKey: operatorKeys.roster,
  queryFn: ({ signal }) => operatorsApi.roster(signal),
});
