// how to connect an ai client, one panel for the project AI page and the
// operator access keys: the clients as a list on the left (each with how it
// signs in), the numbered steps of the picked one on the right. the server
// address is the first step of the connector; a key's commands hold the key
// once one was just created.

import { cn } from "@veristar/brand/cn";
import { type KeyboardEvent, type ReactNode, useId, useState } from "react";
import { clientSnippets, mcpEndpoint } from "@/api/mcp";
import { CodeBlock, CopyField } from "@/components/code-block";

export type ConnectClientId = "connector" | "claude-code" | "cursor";

const CLIENTS: { value: ConnectClientId; name: string; how: string }[] = [
  { value: "connector", name: "Claude and ChatGPT", how: "sign in, no key" },
  { value: "claude-code", name: "Claude Code", how: "terminal, with a key" },
  { value: "cursor", name: "Cursor", how: "mcp.json, with a key" },
];

export function ConnectClient({
  approve,
  keyName,
  createdKey = null,
  shownKey,
  keyStep,
  title = "Connect a client",
  className,
}: {
  // what the person approves after signing in ("the operator view")
  approve: string;
  // the key the client steps ask for ("an AI assistant key")
  keyName: string;
  // a key just created: the copied commands carry it whole
  createdKey?: string | null;
  // the key as the commands show it on screen, such as its start only;
  // the whole key by default
  shownKey?: string | null;
  // the body of the first step of a client that takes a key, such as a
  // form that creates one there and then shows it once
  keyStep?: ReactNode;
  title?: string;
  className?: string;
}) {
  const [client, setClient] = useState<ConnectClientId>("connector");
  const headingId = useId();
  const origin = window.location.origin;
  const s = clientSnippets(origin, createdKey);
  const shown = clientSnippets(origin, shownKey === undefined ? createdKey : shownKey);
  // the client list is a single choice, moved with the arrow keys as in a
  // native radio group
  function key(e: KeyboardEvent<HTMLButtonElement>, i: number) {
    const d =
      e.key === "ArrowDown" || e.key === "ArrowRight"
        ? 1
        : e.key === "ArrowUp" || e.key === "ArrowLeft"
          ? -1
          : 0;
    if (!d) return;
    e.preventDefault();
    const j = (i + d + CLIENTS.length) % CLIENTS.length;
    const next = CLIENTS[j];
    if (!next) return;
    setClient(next.value);
    (e.currentTarget.parentElement?.children[j] as HTMLElement | undefined)?.focus();
  }
  const keyFirst = (
    <Step n={1} title={`Create ${keyName}`}>
      {keyStep}
    </Step>
  );
  return (
    <section aria-labelledby={headingId} className={cn("flex min-w-0 flex-col gap-3", className)}>
      <h2 id={headingId} className="text-[15px] leading-5 font-semibold text-fg">
        {title}
      </h2>
      <div className="grid min-w-0 overflow-hidden rounded-2xl border border-line bg-raised md:grid-cols-[16.25rem_minmax(0,1fr)]">
        <div
          role="radiogroup"
          aria-label="Client"
          className="flex min-w-0 gap-1 border-line p-3 max-md:overflow-x-auto max-md:border-b md:flex-col md:border-r"
        >
          {CLIENTS.map((c, i) => {
            const on = c.value === client;
            return (
              // biome-ignore lint/a11y/useSemanticElements: a client card keyed like a native radio group
              <button
                key={c.value}
                type="button"
                role="radio"
                aria-checked={on}
                tabIndex={on ? 0 : -1}
                onClick={() => setClient(c.value)}
                onKeyDown={(e) => key(e, i)}
                className={cn(
                  "flex min-w-0 shrink-0 flex-col items-start gap-0.5 rounded-xl border px-3.5 py-3 text-left transition-colors duration-(--vs-duration-press) ease-standard focus-ring",
                  on ? "border-line-focus bg-hover" : "border-transparent hover:bg-hover",
                )}
              >
                <span className="text-body font-medium text-fg">{c.name}</span>
                <span className="text-small text-fg-3">{c.how}</span>
              </button>
            );
          })}
        </div>
        <div className="min-w-0 p-6 max-sm:p-4">
          {client === "connector" ? (
            <Steps>
              <Step n={1} title="Copy the server address">
                <CopyField value={mcpEndpoint(origin)} label="server address" className="min-w-0" />
              </Step>
              <Step n={2} title="Add it as a custom connector">
                {/* a plain line sits close under its title, closer than a field */}
                <span className="-mt-1.5 text-small text-fg-2">
                  Settings, Connectors, Add custom connector
                </span>
              </Step>
              <Step n={3} title={`Sign in to Veristar and approve ${approve}`} />
            </Steps>
          ) : client === "claude-code" ? (
            <Steps>
              {keyFirst}
              <Step n={2} title="Run the command with the key">
                <CodeBlock
                  code={shown.claudeCode}
                  copyText={s.claudeCode}
                  label="Terminal"
                  copyLabel="Copy command"
                  wrap
                />
              </Step>
              <Step n={3} title="Or sign in through the browser, without a key">
                <CodeBlock
                  code={s.claudeCodeOAuth}
                  label="Terminal"
                  copyLabel="Copy command"
                  wrap
                />
              </Step>
            </Steps>
          ) : (
            <Steps>
              {keyFirst}
              <Step
                n={2}
                title={
                  <>
                    Add it to <code className="font-mono text-[13px]">.cursor/mcp.json</code>
                  </>
                }
              >
                <CodeBlock
                  code={shown.cursor}
                  copyText={s.cursor}
                  label="mcp.json"
                  copyLabel="Copy configuration"
                />
              </Step>
              {createdKey ? null : (
                <Step n={3} title="Put the key in place of the angle brackets" />
              )}
            </Steps>
          )}
        </div>
      </div>
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
