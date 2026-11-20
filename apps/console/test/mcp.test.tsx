import { describe, expect, it } from "vitest";
import { clientSnippets, mcpCredential, mcpCredentialCreated } from "@/api/mcp";
import { CreateKey, credentialEnd, validateMcpKey } from "@/pages/project/ai";
import { byLabel, byRole, click, render, stubFetch, type, waitFor } from "./helpers";

const credential = {
  id: "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a10",
  kind: "key",
  scope: "tenant",
  tenant_id: "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a11",
  name: "laptop",
  prefix: "sa_mcp_Ab3x",
  client_id: null,
  user_id: "0193a6f2-7c1e-7d40-9f55-2b1c2a5e8a12",
  github_login: "octocat",
  display_name: "octocat",
  status: "active",
  created_at: "2026-11-14T05:00:00Z",
  expires_at: "2027-02-12T05:00:00Z",
  revoked_at: null,
  revoked_reason: null,
  last_seen_at: null,
};

describe("mcp access keys", () => {
  it("checks the key fields before sending", () => {
    expect(validateMcpKey({ name: " ", days: "90" }).errors.name).toBe("Enter a name.");
    expect(validateMcpKey({ name: "x".repeat(101), days: "90" }).errors.name).toBe(
      "Use at most 100 characters.",
    );
    expect(validateMcpKey({ name: "a\u0001b", days: "90" }).errors.name).toBe(
      "Remove control characters.",
    );
    expect(validateMcpKey({ name: "ok", days: "366" }).errors.days).toBeDefined();
    expect(validateMcpKey({ name: "ok", days: "1.5" }).errors.days).toBeDefined();
    expect(validateMcpKey({ name: " laptop ", days: "30" }).body).toEqual({
      name: "laptop",
      expires_in_days: 30,
    });
  });

  it("parses the contract objects", () => {
    expect(mcpCredential.parse(credential).status).toBe("active");
    expect(() => mcpCredentialCreated.parse({ ...credential, key: "sa_op_x" })).toThrow();
    expect(
      mcpCredentialCreated.parse({ ...credential, key: `sa_mcp_${"A".repeat(40)}` }).key,
    ).toMatch(/^sa_mcp_/);
  });

  it("writes copy-ready client configurations", () => {
    const s = clientSnippets("https://app.example.org", "sa_mcp_KEY");
    expect(s.claudeCode).toContain(
      "claude mcp add --transport http veristar https://app.example.org/mcp",
    );
    expect(s.claudeCode).toContain('--header "Authorization: Bearer sa_mcp_KEY"');
    expect(s.claudeCodeOAuth).toBe(
      "claude mcp add --transport http veristar https://app.example.org/mcp",
    );
    expect(JSON.parse(s.cursor)).toEqual({
      mcpServers: {
        veristar: {
          url: "https://app.example.org/mcp",
          headers: { Authorization: "Bearer sa_mcp_KEY" },
        },
      },
    });
    expect(s.connectorUrl).toBe("https://app.example.org/mcp");
    expect(clientSnippets("https://app.example.org", null).claudeCode).toContain("<your key>");
  });

  it("creates a key and shows the server's field errors", async () => {
    const created: unknown[] = [];
    const calls = stubFetch([
      {
        status: 422,
        body: {
          error: {
            code: "validation_failed",
            message: "invalid",
            fields: { name: "too_long" },
            request_id: "r",
          },
        },
      },
      { status: 201, body: { ...credential, key: `sa_mcp_${"B".repeat(40)}` } },
    ]);
    const host = await render(
      <CreateKey
        create={async (b) => {
          const { mcpApi } = await import("@/api/mcp");
          return mcpApi.createTenantKey(credential.tenant_id, b);
        }}
        onCreated={(k) => created.push(k)}
      />,
    );
    // the form sits in the client's first step: a name and the days until
    // it ends on one line
    await type(byLabel(host, "Name"), "laptop");
    await click(byRole(host, "button", "Create key"));
    await waitFor(() => expect(host.textContent).toContain("Use at most 100 characters."));
    await click(byRole(host, "button", "Create key"));
    await waitFor(() => expect(created).toHaveLength(1));
    expect(calls[1]?.url).toBe(`/api/tenants/${credential.tenant_id}/mcp-credentials`);
    expect(calls[1]?.method).toBe("POST");
    expect(calls[1]?.headers.get("X-Requested-With")).toBe("sa-console");
    expect(calls[1]?.body).toEqual({ name: "laptop", expires_in_days: 90 });
  });
});

describe("mcp key list", () => {
  it("says when a key ends, with the reason of a revocation", () => {
    const c = mcpCredential.parse(credential);
    expect(credentialEnd(c)).toBe("Feb 12, 2027");
    const revoked = mcpCredential.parse({
      ...credential,
      status: "revoked",
      revoked_at: "2026-11-15T05:00:00Z",
      revoked_reason: "member_removed",
    });
    expect(credentialEnd(revoked)).toBe(
      "Revoked Nov 15, 2026, 05:00 UTC: its user left the project",
    );
  });
});
