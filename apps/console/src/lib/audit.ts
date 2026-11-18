// the audit log in plain language: who did what to which object, the
// changes of an entry, and runs of identical system events folded into one
// line. pure, so tests read it directly.

import type { AuditEntry, SiteStatus, TenantStatus } from "@/api/schemas";
import { statusEntry } from "@/components/status";
import { formatCount, formatDateRange, MISSING } from "@/lib/format";
import { shortAddress } from "@/lib/stellar-wallet";

// ---------------------------------------------------------------- days

// the audit filter takes rfc 3339 in utc (contract 5.9), so a day is read
// as a utc day. "to" is the start of the next day, so the chosen day counts.
export function dayToUtc(day: string, end: boolean): string {
  if (day === "") return "";
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return "";
  if (end) d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function utcToDay(iso: string, end: boolean): string {
  if (iso === "") return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  if (end) d.setUTCDate(d.getUTCDate() - 1);
  return d.toISOString().slice(0, 10);
}

// 5.9: from before to, at most 400 days apart; the server refuses anything
// else, so the page says it first. both are days, "to" included.
export function auditDaysError(from: string, to: string): string | null {
  if (!from || !to) return null;
  if (to < from) return "End on or after the start day.";
  if ((Date.parse(to) - Date.parse(from)) / 86_400_000 + 1 > 400) return "Choose at most 400 days.";
  return null;
}

// ------------------------------------------------------------- families

// one choice narrows the log to a family of actions (the action filter
// takes a prefix that ends in a dot)
export const ACTION_FAMILIES: { value: string; label: string }[] = [
  { value: "tenant.", label: "Projects" },
  { value: "site.", label: "Sites" },
  { value: "member.", label: "Members" },
  { value: "invite.", label: "Invites" },
  { value: "erasure.", label: "Erasure" },
  { value: "operator.", label: "Operators" },
  { value: "settings.", label: "Settings" },
  { value: "cohort.", label: "Cohorts" },
  { value: "rule.", label: "Rules" },
  { value: "report.", label: "Reports" },
  { value: "onchain.", label: "On-chain ids" },
  { value: "export.", label: "Exports" },
  { value: "mcp.", label: "AI assistant calls" },
  { value: "mcp_credential.", label: "AI assistant keys" },
  { value: "operator_api_key.", label: "Machine API keys" },
  { value: "otlp_token.", label: "API tokens" },
  { value: "purge.", label: "Data deletion" },
  { value: "user.", label: "Sign-in" },
];

// ---------------------------------------------------------------- actors

export type ActorKind = "person" | "system" | "assistant";

export function actorKind(e: AuditEntry): ActorKind {
  if (e.actor_login) return "person";
  if (e.actor.startsWith("mcp_credential:")) return "assistant";
  if (e.actor.startsWith("system:") || e.actor === "system") return "system";
  return "person";
}

// a person by their login; an ai assistant by what it is; the system by its
// job in words. a person without a login waits for a display name from the
// server, so the line says "A person" and the id stays in the details.
export function actorName(e: AuditEntry): string {
  if (e.actor_login) return e.actor_login;
  const kind = actorKind(e);
  if (kind === "assistant") return "An AI assistant";
  if (kind === "system") {
    const job = e.actor.slice("system:".length).replaceAll(/[-_]/g, " ").trim();
    return job ? `The system (${job})` : "The system";
  }
  return "A person";
}

export function initials(login: string): string {
  return (
    login
      .replace(/[^0-9A-Za-z]/g, "")
      .slice(0, 2)
      .toUpperCase() || "?"
  );
}

// ----------------------------------------------------------------- verbs

// the action as the verb of a sentence about its target
const VERB: Record<string, string> = {
  "tenant.create": "enrolled the project",
  "tenant.update": "edited the project",
  "tenant.resubmit": "resubmitted the project",
  "tenant.approve": "approved the project",
  "tenant.reject": "did not approve the project",
  "tenant.suspend": "suspended the project",
  "tenant.resume": "resumed the project",
  "tenant.remove": "removed the project",
  "tenant.terms_accept": "accepted the terms for the project",
  "site.create": "added the site",
  "site.update": "edited the site",
  "site.delete": "deleted the site",
  "site.verify": "verified the domain of the site",
  "site.approve": "approved the site",
  "site.reject": "did not approve the site",
  "site.stellar_toml": "checked the stellar.toml of the site",
  "site.review_queue": "sent to review the site",
  "member.add": "added the member",
  "member.remove": "removed the member",
  "member.role_change": "changed the role of",
  "invite.create": "invited",
  "invite.revoke": "cancelled the invite of",
  "invite.resolve": "closed the invite of",
  "erasure.request": "asked to erase data of",
  "erasure.complete": "erased data of",
  "erasure.fail": "could not erase data of",
  "purge.step": "deleted part of the data of",
  "purge.complete": "finished deleting the data of",
  "mcp.tool_call": "used an AI tool on",
  "mcp_credential.create": "created the AI assistant key",
  "mcp_credential.revoke": "revoked the AI assistant key",
  "operator_api_key.create": "created the machine API key",
  "operator_api_key.revoke": "revoked the machine API key",
  "otlp_token.create": "created the API token",
  "otlp_token.revoke": "revoked the API token",
  "settings.enrollment_mode": "changed the enrollment mode",
  "settings.removal_keep_aggregates": "changed what removal keeps",
  "umami.password_rotate": "changed the web analytics passwords",
  "user.sign_in": "signed in",
  "user.sign_out": "signed out",
  "user.identity_link": "added a sign-in method to",
  "user.identity_unlink": "removed a sign-in method from",
  "user.operator_sync": "updated the operator access of",
  "operator.add": "added the operator",
  "operator.bootstrap": "added from the deploy settings the operator",
  "operator.remove": "removed the operator",
  "operator.suspend": "suspended the operator",
  "operator.resume": "resumed the operator",
  "operator.role_change": "changed the operator role of",
  "operator.passkey_add": "added an operator passkey",
  "operator.passkey_delete": "deleted an operator passkey",
  "operator.passkeys_reset": "reset the passkeys of",
  "operator.step_up": "confirmed with a passkey",
  "cohort.set": "set the cohort of",
  "cohort.propose": "proposed a cohort for",
  "cohort.confirm": "confirmed the cohort of",
  "cohort.proposal_dismiss": "dismissed the cohort proposal of",
  "rule.create": "created the rule",
  "rule.update": "edited the rule",
  "rule.delete": "deleted the rule",
  "report.create": "signed a report for",
  "report.revoke": "revoked a report link of",
  "report.view": "opened the report",
  "onchain.add": "declared an on-chain id for",
  "onchain.remove": "removed an on-chain id from",
  "onchain.challenge": "started the on-chain check for",
  "onchain.verify": "verified an on-chain id of",
  "export.csv": "exported a CSV file of",
  // the setup steps the service runs for a project
  "provision.team_create": "set up analytics for",
  "provision.user_create": "created an analytics account for",
  "provision.user_update": "updated the analytics account of",
  "provision.membership_add": "gave analytics access to",
  "provision.membership_remove": "removed analytics access from",
  "provision.website_create": "created the analytics site of",
  "provision.website_update": "updated the analytics site of",
  "provision.website_delete": "deleted the analytics site of",
  "provision.registry_put": "opened event intake for",
  "provision.registry_delete": "closed event intake for",
  "provision.otlp_registry_put": "opened API data intake for",
};

// an action this list does not know keeps its code, so nothing is hidden
export function auditVerb(action: string): string {
  return VERB[action] ?? `recorded the action ${action} on`;
}

function detailText(e: AuditEntry, key: string): string | null {
  const v = e.detail?.[key];
  return typeof v === "string" && v !== "" ? v : null;
}

// the words for a target without a name in the detail
const TARGET_KIND: Record<string, string> = {
  tenant: "a project",
  site: "a site",
  user: "a person",
  operator: "an operator",
  rule: "a rule",
  report: "a report",
  rule_set: "a rule",
  mcp_credential: "an AI assistant key",
  operator_api_key: "a machine API key",
  otlp_token: "an API token",
  chain_id: "an on-chain id",
  invite: "an invite",
  mcp_client: "an AI client",
};

export type AuditSentence = {
  actor: string;
  kind: ActorKind;
  verb: string;
  // the object as a person reads it, or null when the action has none
  object: string | null;
  // the project page the object opens, if the object is a project
  href: string | null;
};

const PROJECT = /^tenant:([0-9a-f-]{36})$/;

// a check that did not pass reads "checked", never "verified"
const CHECKS: Record<string, string> = {
  "site.verify": "checked the domain of the site",
  "onchain.verify": "checked an on-chain id of",
};

// the name of the object from the detail of the entry: a name, a domain, a
// login, an on-chain id or the range of a report
function nameOf(e: AuditEntry): string | null {
  const strkey = detailText(e, "strkey");
  const from = detailText(e, "from");
  const to = detailText(e, "to");
  return (
    detailText(e, "name") ??
    detailText(e, "domain") ??
    detailText(e, "github_login") ??
    (strkey ? shortAddress(strkey) : null) ??
    (e.target.startsWith("report:") && from && to ? formatDateRange(from, to) : null)
  );
}

// names maps a target ("site:<id>", "tenant:<id>") to the name a page
// already knows, for entries whose detail does not carry it
export function auditSentence(
  e: AuditEntry,
  names: Readonly<Record<string, string>> = {},
): AuditSentence {
  const tool = e.action === "mcp.tool_call" ? detailText(e, "tool") : null;
  const failed = e.action in CHECKS && detailText(e, "result") !== "ok";
  let verb = tool
    ? `used the AI tool ${tool} on`
    : failed
      ? (CHECKS[e.action] as string)
      : auditVerb(e.action);
  const named = nameOf(e) ?? names[e.target] ?? null;
  const prefix = e.target.split(":")[0] ?? "";
  let object: string | null = e.target ? (named ?? TARGET_KIND[prefix] ?? "an object") : null;
  // a verb that names its own object ("the project") reads "a project" twice
  // without a name, so the article goes
  if (object && !named && / the (project|site|member|operator|rule|report)$/.test(verb)) {
    verb = verb.replace(/ the (project|site|member|operator|rule|report)$/, "");
  }
  // an action on the actor itself (a sign-in) or on a setting, whose verb
  // names it, reads without a second object
  if (!e.target || e.target === e.actor || e.action.startsWith("settings.")) {
    verb = verb.replace(/ (on|of|for|from|to)$/, "");
    object = null;
  }
  const m = PROJECT.exec(e.target);
  return {
    actor: actorName(e),
    kind: actorKind(e),
    verb,
    object,
    href: m ? `/operator/projects/${m[1]}` : null,
  };
}

// --------------------------------------------------------------- changes

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// the states of a change in the words the status badges use, so the log
// and the pills never disagree ("Not approved", not "rejected")
const VERIFICATION_WORD: Record<string, string> = {
  verified: "Verified",
  unverified: "Not verified",
  unchecked: "Not checked",
};

function stateWord(key: string, target: string, v: unknown): string | null {
  if (typeof v !== "string") return null;
  if (key === "status") {
    const kind = target.startsWith("site:") ? "site" : "project";
    const e =
      kind === "site"
        ? statusEntry({ kind, status: v as SiteStatus })
        : statusEntry({ kind, status: v as TenantStatus });
    return e?.word ?? null;
  }
  if (key === "verification") return VERIFICATION_WORD[v] ?? null;
  return null;
}

// one side of a change as a person reads it: a list or an object is spelt
// out (never "[object Object]"), a missing value is the missing glyph
export function changeValue(v: unknown): string {
  if (v === null || v === undefined || v === "") return MISSING;
  if (typeof v === "boolean") return v ? "on" : "off";
  if (typeof v === "number") return formatCount(v);
  if (Array.isArray(v)) return v.length ? v.map(changeValue).join(", ") : MISSING;
  if (isObj(v)) {
    const parts = Object.entries(v).map(([k, x]) => `${k.replaceAll("_", " ")} ${changeValue(x)}`);
    return parts.length ? parts.join(", ") : MISSING;
  }
  return String(v);
}

// every { old, new } pair of the detail that changed something; a pair
// with the same value on both sides says nothing and is left out
export function auditChanges(e: AuditEntry): { key: string; from: string; to: string }[] {
  const out: { key: string; from: string; to: string }[] = [];
  for (const [key, v] of Object.entries(e.detail ?? {})) {
    if (!isObj(v) || !("old" in v) || !("new" in v)) continue;
    const from = stateWord(key, e.target, v.old) ?? changeValue(v.old);
    const to = stateWord(key, e.target, v.new) ?? changeValue(v.new);
    if (from !== to) out.push({ key: key.replaceAll("_", " "), from, to });
  }
  return out;
}

export function auditReason(e: AuditEntry): string | null {
  return detailText(e, "reason");
}

// ------------------------------------------------------------------ runs

export type AuditItem =
  | { kind: "one"; entry: AuditEntry }
  | { kind: "run"; key: string; entries: AuditEntry[] };

// the shortest run of identical system events that folds into one line
export const RUN_MIN = 3;

// consecutive events of the same system job and action (a nightly check of
// every domain, say) fold into one line that opens to the single events.
// people's actions never fold: each one is a decision someone made.
export function foldRuns(entries: AuditEntry[]): AuditItem[] {
  const out: AuditItem[] = [];
  let i = 0;
  while (i < entries.length) {
    const e = entries[i] as AuditEntry;
    let j = i + 1;
    if (actorKind(e) === "system") {
      while (j < entries.length) {
        const n = entries[j] as AuditEntry;
        if (n.actor !== e.actor || n.action !== e.action || n.at.slice(0, 10) !== e.at.slice(0, 10))
          break;
        j++;
      }
    }
    if (j - i >= RUN_MIN) out.push({ kind: "run", key: `${e.id}`, entries: entries.slice(i, j) });
    else for (const x of entries.slice(i, j)) out.push({ kind: "one", entry: x });
    i = j;
  }
  return out;
}
