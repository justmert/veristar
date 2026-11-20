// how to connect an ai client, one panel for the project AI page and the
// operator access keys: the clients as a list on the left (each with how it
// signs in), the numbered steps of the picked one on the right. the server
// address is the first step of the connector; a key's commands hold the key
// once one was just created.

import { cn } from "@veristar/brand/cn";
import { Tabs } from "radix-ui";
import type { ReactNode } from "react";
import { clientSnippets, mcpEndpoint } from "@/api/mcp";
import { CodeBlock, CopyField } from "@/components/code-block";

type Client = "connector" | "claude-code" | "cursor";

const CLIENTS: { value: Client; name: string; how: string }[] = [
  { value: "connector", name: "Claude and ChatGPT", how: "sign in, no key" },
  { value: "claude-code", name: "Claude Code", how: "terminal, with a key" },
  { value: "cursor", name: "Cursor", how: "mcp.json, with a key" },
];

export function ConnectClient({
  approve,
  keyName,
  createdKey = null,
  title = "Connect a client",
  className,
}: {
  // what the person approves after signing in ("the operator view")
  approve: string;
  // the key the client steps ask for ("an AI assistant key")
  keyName: string;
  // a key just created: the commands carry it
  createdKey?: string | null;
  title?: string;
  className?: string;
}) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, createdKey);
  return (
    <section
      aria-labelledby="connect-client-h"
      className={cn("flex min-w-0 flex-col gap-3", className)}
    >
      <h2 id="connect-client-h" className="text-[15px] leading-5 font-semibold text-fg">
        {title}
      </h2>
      <Tabs.Root
        defaultValue="connector"
        orientation="vertical"
        className="grid min-w-0 overflow-hidden rounded-2xl border border-line bg-raised md:grid-cols-[16.25rem_minmax(0,1fr)]"
      >
        <Tabs.List
          aria-label="Client"
          className="flex min-w-0 gap-1 border-line p-3 max-md:overflow-x-auto max-md:border-b md:flex-col md:border-r"
        >
          {CLIENTS.map((c) => (
            <Tabs.Trigger
              key={c.value}
              value={c.value}
              className="flex min-w-0 shrink-0 flex-col items-start gap-0.5 rounded-xl border border-transparent px-3.5 py-3 text-left transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring data-[state=active]:border-line-focus data-[state=active]:bg-raised"
            >
              <span className="text-body font-medium text-fg">{c.name}</span>
              <span className="text-small text-fg-3">{c.how}</span>
            </Tabs.Trigger>
          ))}
        </Tabs.List>
        <Tabs.Content value="connector" className="min-w-0 p-6 focus-ring">
          <Steps>
            <Step n={1} title="Copy the server address">
              <CopyField value={mcpEndpoint(origin)} label="server address" className="min-w-0" />
            </Step>
            <Step n={2} title="Add it as a custom connector">
              <span className="text-small text-fg-2">
                Settings, Connectors, Add custom connector
              </span>
            </Step>
            <Step n={3} title={`Sign in to Veristar and approve ${approve}`} />
          </Steps>
        </Tabs.Content>
        <Tabs.Content value="claude-code" className="min-w-0 p-6 focus-ring">
          <Steps>
            <Step n={1} title={`Create ${keyName}`} />
            <Step n={2} title="Run the command with the key">
              <CodeBlock code={s.claudeCode} label="Terminal" copyLabel="Copy command" wrap />
            </Step>
            <Step n={3} title="Or sign in through the browser, without a key">
              <CodeBlock code={s.claudeCodeOAuth} label="Terminal" copyLabel="Copy command" wrap />
            </Step>
          </Steps>
        </Tabs.Content>
        <Tabs.Content value="cursor" className="min-w-0 p-6 focus-ring">
          <Steps>
            <Step n={1} title={`Create ${keyName}`} />
            <Step
              n={2}
              title={
                <>
                  Add it to <code className="font-mono text-[13px]">.cursor/mcp.json</code>
                </>
              }
            >
              <CodeBlock code={s.cursor} label="mcp.json" copyLabel="Copy configuration" />
            </Step>
            {createdKey ? null : <Step n={3} title="Put the key in place of the angle brackets" />}
          </Steps>
        </Tabs.Content>
      </Tabs.Root>
    </section>
  );
}

function Steps({ children }: { children: ReactNode }) {
  return <ol className="flex min-w-0 list-none flex-col gap-5">{children}</ol>;
}

function Step({ n, title, children }: { n: number; title: ReactNode; children?: ReactNode }) {
  return (
    <li className="grid min-w-0 grid-cols-[1.5rem_minmax(0,1fr)] gap-x-4">
      <span
        aria-hidden
        className="flex size-6 items-center justify-center rounded-full border border-line-strong text-caption font-medium text-fg-2"
      >
        {n}
      </span>
      <div className="flex min-w-0 flex-col gap-2.5 pt-0.5">
        <span className="text-body font-medium text-fg">{title}</span>
        {children}
      </div>
    </li>
  );
}
