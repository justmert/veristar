import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type McpCredentialCreated, mcpApi, mcpKeys, operatorMcpQuery } from "@/api/mcp";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { Connect, CreatedKeyPanel, CredentialList, McpKeyCreateForm } from "@/pages/mcp";

// the operator area's "AI / MCP" section (contract 12.8): keys that open the
// operator tools (global web and api views, project search and compare)
// to an AI client, read-only, acting as the operator who made them.
export function OperatorMcpPage() {
  const list = useQuery(operatorMcpQuery);
  const qc = useQueryClient();
  const [created, setCreated] = useState<McpCredentialCreated | null>(null);
  return (
    <section className="space-y-6">
      <header>
        <h2 className="text-xl font-semibold tracking-tight">AI assistants (MCP)</h2>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          An operator key gives an AI client the global view: search projects, rank every project on
          web and API metrics, and compare up to five side by side. It reads the same daily
          aggregates as the Global web, Global API and Compare pages, never sessions, and changes
          nothing. It stops working when its operator loses operator rights. Connectors can also be
          approved for the operator view on the consent page.
        </p>
      </header>
      <Connect created={created} />
      {created ? (
        <CreatedKeyPanel created={created} onDone={() => setCreated(null)} />
      ) : (
        <McpKeyCreateForm
          label="Create an operator key"
          create={(b) => mcpApi.createOperatorKey(b)}
          onCreated={(k) => {
            qc.invalidateQueries({ queryKey: mcpKeys.operator });
            setCreated(k);
          }}
        />
      )}
      <section aria-labelledby="op-mcp-list" className="space-y-3">
        <h3 id="op-mcp-list" className="font-semibold">
          Operator keys and connections
        </h3>
        {list.isPending ? <Loading label="Loading keys" /> : null}
        {list.error ? <ErrorPanel error={list.error} onRetry={() => list.refetch()} /> : null}
        {list.data ? (
          <CredentialList
            items={list.data}
            canRevoke={() => true}
            revoke={(c) => mcpApi.revokeOperatorCredential(c.id)}
            invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.operator })}
          />
        ) : null}
      </section>
    </section>
  );
}
