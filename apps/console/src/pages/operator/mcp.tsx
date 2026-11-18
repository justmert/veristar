import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { type McpCredentialCreated, mcpApi, mcpKeys, operatorMcpQuery } from "@/api/mcp";
import { Panel } from "@/components/panel";
import { Connect, CreatedKeyPanel, CredentialSection, McpKeyCreateForm } from "@/pages/project/ai";

// the operator area's "AI / MCP" section (contract 12.8): keys that open the
// operator tools (global web and api views, project search and compare)
// to an AI client, read-only, acting as the operator who made them. the
// operator layout draws the place header and its tabs.
export function OperatorMcpPage() {
  const list = useQuery(operatorMcpQuery);
  const qc = useQueryClient();
  const [created, setCreated] = useState<McpCredentialCreated | null>(null);
  return (
    <section
      aria-labelledby="op-mcp-h"
      className="flex max-w-[62.5rem] animate-rise-in flex-col gap-6"
    >
      <header className="flex flex-col gap-1">
        <h2 id="op-mcp-h" className="text-section text-fg">
          AI assistants (MCP)
        </h2>
        <p className="max-w-prose text-body text-fg-2">
          An operator key gives an AI client the global view: search projects, rank every project on
          web and API metrics, and compare up to five side by side. It reads the same daily
          aggregates as the Projects and Compare pages, never sessions, and changes nothing. It
          stops working when its operator loses operator rights. Connectors can also be approved for
          the operator view on the consent page.
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
      <Panel
        aria-labelledby="op-mcp-list"
        title={<span id="op-mcp-list">Operator keys and connections</span>}
        flush
      >
        <CredentialSection
          list={list}
          canRevoke={() => true}
          revoke={(c) => mcpApi.revokeOperatorCredential(c.id)}
          invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.operator })}
        />
      </Panel>
    </section>
  );
}
