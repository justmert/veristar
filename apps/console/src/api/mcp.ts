// the console side of the mcp server (docs/reference/control-plane-api.md
// section 12): access keys of a project, operator keys and the oauth
// consent page. kept in one module of its own so the pages that use it stay
// apart from the rest of the console.

import { queryOptions } from "@tanstack/react-query";
import { z } from "zod";
import { request, requestNoContent } from "./client";
import { page } from "./schemas";

const id = z.uuid();
const time = z.iso.datetime();

// 12.8 credential: an access key made here, or an oauth grant of a connector
export const mcpCredential = z.object({
  id,
  kind: z.enum(["key", "oauth"]),
  scope: z.enum(["tenant", "operator"]),
  tenant_id: id.nullable(),
  name: z.string(),
  prefix: z.string().nullable(),
  client_id: z.string().nullable(),
  user_id: id,
  github_login: z.string().nullable(),
  status: z.enum(["active", "expired", "revoked"]),
  created_at: time,
  expires_at: time.nullable(),
  revoked_at: time.nullable(),
  revoked_reason: z.string().nullable(),
  last_seen_at: time.nullable(),
});
export type McpCredential = z.infer<typeof mcpCredential>;

export const mcpCredentialCreated = mcpCredential.extend({ key: z.string().startsWith("sa_mcp_") });
export type McpCredentialCreated = z.infer<typeof mcpCredentialCreated>;

const mcpCredentialPage = page(mcpCredential);

// 12.3 the consent page's view of an authorization request
export const consentRequest = z.object({
  client_name: z.string(),
  client_id: z.string(),
  redirect_origin: z.string(),
  scope: z.string(),
  resource: z.string(),
  expires_at: time,
  tenants: z.array(
    z.object({
      id,
      slug: z.string(),
      name: z.string(),
      status: z.string(),
      role: z.string(),
    }),
  ),
  operator: z.boolean(),
});
export type ConsentRequest = z.infer<typeof consentRequest>;

const redirectAnswer = z.object({ redirect_to: z.string().min(1) });

export type McpKeyInput = { name: string; expires_in_days: number };

// contract 12.8 limits, also stated on the pages
export const MCP_KEY_DEFAULT_DAYS = 90;
export const MCP_KEY_MAX_DAYS = 365;

const enc = encodeURIComponent;

async function all(path: string, signal?: AbortSignal): Promise<McpCredential[]> {
  const out: McpCredential[] = [];
  let cursor: string | null = null;
  do {
    const q: string = cursor ? `&cursor=${enc(cursor)}` : "";
    const p: { items: McpCredential[]; next_cursor: string | null } = await request(
      "GET",
      `${path}?limit=200${q}`,
      mcpCredentialPage,
      undefined,
      signal,
    );
    out.push(...p.items);
    cursor = p.next_cursor;
  } while (cursor !== null);
  return out;
}

export const mcpApi = {
  tenantCredentials: (tenantId: string, signal?: AbortSignal) =>
    all(`/tenants/${enc(tenantId)}/mcp-credentials`, signal),
  createTenantKey: (tenantId: string, body: McpKeyInput) =>
    request("POST", `/tenants/${enc(tenantId)}/mcp-credentials`, mcpCredentialCreated, body),
  revokeTenantCredential: (tenantId: string, credentialId: string) =>
    requestNoContent("DELETE", `/tenants/${enc(tenantId)}/mcp-credentials/${enc(credentialId)}`),

  operatorCredentials: (signal?: AbortSignal) => all("/operator/mcp-credentials", signal),
  createOperatorKey: (body: McpKeyInput) =>
    request("POST", "/operator/mcp-credentials", mcpCredentialCreated, body),
  revokeOperatorCredential: (credentialId: string) =>
    requestNoContent("DELETE", `/operator/mcp-credentials/${enc(credentialId)}`),

  inspect: (req: string) =>
    request("POST", "/mcp/authorize/inspect", consentRequest, { request: req }),
  approve: (req: string, scope: "tenant" | "operator", tenantId: string | null) =>
    request("POST", "/mcp/authorize/approve", redirectAnswer, {
      request: req,
      scope,
      tenant_id: tenantId,
    }),
  deny: (req: string) => request("POST", "/mcp/authorize/deny", redirectAnswer, { request: req }),
};

export const mcpKeys = {
  tenant: (tenantId: string) => ["tenant", tenantId, "mcp-credentials"] as const,
  operator: ["operator", "mcp-credentials"] as const,
  consent: (req: string) => ["mcp-consent", req] as const,
};

export const tenantMcpQuery = (tenantId: string) =>
  queryOptions({
    queryKey: mcpKeys.tenant(tenantId),
    queryFn: ({ signal }) => mcpApi.tenantCredentials(tenantId, signal),
  });

export const operatorMcpQuery = queryOptions({
  queryKey: mcpKeys.operator,
  queryFn: ({ signal }) => mcpApi.operatorCredentials(signal),
});

// the mcp endpoint of this console's origin (app.<BASE_DOMAIN>/mcp)
export function mcpEndpoint(origin: string): string {
  return `${origin}/mcp`;
}

// copy-ready client configurations. a key goes in an Authorization header;
// clients that support oauth need only the url.
export function clientSnippets(origin: string, key: string | null) {
  const url = mcpEndpoint(origin);
  const secret = key ?? "<your key>";
  return {
    claudeCode: `claude mcp add --transport http veristar ${url} \\\n  --header "Authorization: Bearer ${secret}"`,
    claudeCodeOAuth: `claude mcp add --transport http veristar ${url}`,
    cursor: JSON.stringify(
      { mcpServers: { veristar: { url, headers: { Authorization: `Bearer ${secret}` } } } },
      null,
      2,
    ),
    connectorUrl: url,
  };
}

// why a credential stopped working, in words for the list
export const REVOKED_REASON: Record<string, string> = {
  user: "revoked in the console",
  operator: "revoked by an operator",
  member_removed: "its user left the project",
  tenant_suspended: "the project was suspended",
  tenant_rejected: "the project was not approved",
  tenant_removed: "the project was removed",
  operator_removed: "its user is no longer an operator",
  refresh_reuse: "a refresh token was used twice",
  code_reuse: "an authorization code was used twice",
};
