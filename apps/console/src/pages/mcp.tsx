import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, ShieldCheck } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import {
  clientSnippets,
  MCP_KEY_DEFAULT_DAYS,
  MCP_KEY_MAX_DAYS,
  type McpCredential,
  type McpCredentialCreated,
  type McpKeyInput,
  mcpApi,
  mcpEndpoint,
  mcpKeys,
  REVOKED_REASON,
  tenantMcpQuery,
} from "@/api/mcp";
import { CodeBlock, CopyField } from "@/components/code-block";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading, StateBlock } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader, Panel } from "@/components/panel";
import { OneTimeSecret } from "@/components/secret";
import { Pill, type Tone } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDateTimeUtc } from "@/lib/format";
import { useProject } from "./project-layout";

// the project's "AI / MCP" page (contract 12.8, docs/onboarding/mcp.md):
// access keys for AI clients, the connector url for oauth clients, and the
// list of everything that can read this project through mcp.

export type McpKeyForm = { name: string; days: string };
export type McpKeyErrors = Partial<Record<keyof McpKeyForm, string>>;

function isControl(ch: string): boolean {
  const c = ch.codePointAt(0) ?? 0;
  return c < 0x20 || c === 0x7f;
}

// the field rules of 12.8, checked before sending; the server checks again
export function validateMcpKey(f: McpKeyForm): { errors: McpKeyErrors; body: McpKeyInput | null } {
  const errors: McpKeyErrors = {};
  const name = f.name.trim();
  if (!name) errors.name = "Enter a name.";
  else if ([...name].length > 100) errors.name = "Use at most 100 characters.";
  else if ([...name].some(isControl)) errors.name = "Remove control characters.";
  const days = Number(f.days);
  if (!/^\d+$/.test(f.days) || days < 1 || days > MCP_KEY_MAX_DAYS)
    errors.days = `Enter a whole number of days from 1 to ${MCP_KEY_MAX_DAYS}.`;
  if (Object.keys(errors).length) return { errors, body: null };
  return { errors, body: { name, expires_in_days: days } };
}

function serverErrors(err: unknown): McpKeyErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: McpKeyErrors = {};
  for (const [field, code] of Object.entries(err.fields)) {
    if (field === "name")
      out.name = code === "too_long" ? "Use at most 100 characters." : "Enter a valid name.";
    if (field === "expires_in_days") out.days = `Enter 1 to ${MCP_KEY_MAX_DAYS} days.`;
  }
  return out;
}

export function McpPage() {
  const { tenant, me, isOwner } = useProject();
  const list = useQuery(tenantMcpQuery(tenant.id));
  const [created, setCreated] = useState<McpCredentialCreated | null>(null);
  const qc = useQueryClient();
  const canCreate = tenant.status === "active" || tenant.status === "pending";

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="AI assistants (MCP)"
        description="Ask Claude, ChatGPT, Cursor or any client of the Model Context Protocol about this project's web and API analytics. A connection acts as the person who made it and reads this project only."
      />
      <ul aria-label="What a connection can do" className="-mt-2 flex flex-wrap gap-2">
        {[
          "Every tool is read-only",
          "This project only",
          "Ends when its person leaves or the project is suspended or removed",
        ].map((t) => (
          <li
            key={t}
            className="inline-flex min-h-6 items-center gap-1.5 rounded-full border border-line px-2.5 py-0.5 text-caption text-fg-2"
          >
            <ShieldCheck aria-hidden className="size-3.5 shrink-0 text-success" />
            {t}
          </li>
        ))}
      </ul>

      <div className="flex animate-rise-in flex-col gap-6">
        <Connect created={created} />

        {created ? (
          <CreatedKeyPanel created={created} onDone={() => setCreated(null)} />
        ) : canCreate ? (
          <McpKeyCreateForm
            label="Create an access key"
            create={(b) => mcpApi.createTenantKey(tenant.id, b)}
            onCreated={(k) => {
              qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) });
              setCreated(k);
            }}
          />
        ) : (
          <Notice tone="warning" title={`No new keys while the project is ${tenant.status}`} />
        )}

        <Panel
          aria-labelledby="mcp-list"
          title={<span id="mcp-list">Keys and connections</span>}
          description="Access keys made on this page and connectors approved through sign-in. Members can revoke their own; owners can revoke any."
          flush
        >
          <CredentialSection
            list={list}
            canRevoke={(c) => isOwner || c.user_id === me.user.id}
            revoke={(c) => mcpApi.revokeTenantCredential(tenant.id, c.id)}
            invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) })}
          />
        </Panel>
      </div>
    </div>
  );
}

// the list states inside a flush panel: loading, error, empty, rows
export function CredentialSection({
  list,
  canRevoke,
  revoke,
  invalidate,
}: {
  list: { isPending: boolean; error: unknown; data?: McpCredential[]; refetch: () => unknown };
  canRevoke: (c: McpCredential) => boolean;
  revoke: (c: McpCredential) => Promise<void>;
  invalidate: () => void;
}) {
  if (list.isPending) return <Loading label="Loading keys" className="px-4 pb-4 sm:px-5" />;
  if (list.error)
    return (
      <div className="px-4 pb-4 sm:px-5">
        <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
      </div>
    );
  return (
    <CredentialList
      items={list.data ?? []}
      canRevoke={canRevoke}
      revoke={revoke}
      invalidate={invalidate}
    />
  );
}

// how to connect each client. with a key just made, the snippets hold it.
export function Connect({ created }: { created: McpCredentialCreated | null }) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, created?.key ?? null);
  return (
    <Panel
      aria-labelledby="mcp-connect"
      title={<span id="mcp-connect">Connect a client</span>}
      actions={
        <div className="flex min-w-0 items-center gap-2">
          <span className="shrink-0 text-caption text-fg-3">Server URL</span>
          <CopyField value={mcpEndpoint(origin)} label="URL" className="min-w-0 max-w-full" />
        </div>
      }
    >
      <Tabs defaultValue="connector">
        <TabsList className="max-w-full flex-wrap">
          <TabsTrigger value="connector">Claude and ChatGPT</TabsTrigger>
          <TabsTrigger value="claude-code">Claude Code</TabsTrigger>
          <TabsTrigger value="cursor">Cursor</TabsTrigger>
        </TabsList>
        <TabsContent value="connector" className="flex animate-rise-in flex-col gap-3">
          <p className="max-w-prose text-small text-fg-2">
            In claude.ai or Claude Desktop open Settings, Connectors, Add custom connector; in
            ChatGPT open Settings, Connectors, Create. Paste the server URL and leave the client
            credentials empty. The client opens this console, you sign in with GitHub, choose the
            project and approve. No key is needed.
          </p>
          <CodeBlock code={s.connectorUrl} label="Connector URL" copyLabel="Copy connector URL" />
        </TabsContent>
        <TabsContent value="claude-code" className="flex animate-rise-in flex-col gap-3">
          <CodeBlock
            code={s.claudeCode}
            label="With an access key from this page"
            copyLabel="Copy command"
            wrap
          />
          <CodeBlock
            code={s.claudeCodeOAuth}
            label="Or sign in through the browser instead of a key"
            copyLabel="Copy command"
            wrap
          />
        </TabsContent>
        <TabsContent value="cursor" className="flex animate-rise-in flex-col gap-3">
          <p className="text-small text-fg-2">
            Add to <code className="font-mono text-fg">.cursor/mcp.json</code> or the global{" "}
            <code className="font-mono text-fg">~/.cursor/mcp.json</code>:
          </p>
          <CodeBlock code={s.cursor} label="mcp.json" copyLabel="Copy configuration" />
        </TabsContent>
      </Tabs>
      {created ? null : (
        <p className="mt-3 text-caption text-fg-3">
          Create a key below and the snippets fill it in.
        </p>
      )}
    </Panel>
  );
}

export function McpKeyCreateForm({
  label,
  create,
  onCreated,
}: {
  label: string;
  create: (b: McpKeyInput) => Promise<McpCredentialCreated>;
  onCreated: (k: McpCredentialCreated) => void;
}) {
  const [form, setForm] = useState<McpKeyForm>({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
  const [errors, setErrors] = useState<McpKeyErrors>({});
  const m = useMutation({
    mutationFn: create,
    onSuccess: (k) => {
      setForm({ name: "", days: String(MCP_KEY_DEFAULT_DAYS) });
      onCreated(k);
    },
    onError: (err) => setErrors(serverErrors(err)),
  });
  function submit(e: FormEvent) {
    e.preventDefault();
    const v = validateMcpKey(form);
    setErrors(v.errors);
    if (v.body) m.mutate(v.body);
  }
  return (
    <form
      aria-label={label}
      onSubmit={submit}
      noValidate
      className="flex flex-col gap-3 rounded-xl border border-line bg-raised p-4 sm:px-5"
    >
      <h2 className="text-title text-fg">{label}</h2>
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_11rem_auto] sm:items-start">
        <TextField
          label="Name"
          hint="Where the key is used, for example laptop or Cursor."
          placeholder="laptop"
          maxLength={100}
          className="max-lg:h-(--vs-control-touch)"
          value={form.name}
          error={errors.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
        <TextField
          label="Expires after (days)"
          hint={`${MCP_KEY_DEFAULT_DAYS} by default, at most ${MCP_KEY_MAX_DAYS}.`}
          type="number"
          inputMode="numeric"
          min={1}
          max={MCP_KEY_MAX_DAYS}
          className="tabular max-lg:h-(--vs-control-touch)"
          value={form.days}
          error={errors.days}
          onChange={(e) => setForm({ ...form, days: e.target.value })}
        />
        <Button
          type="submit"
          variant="outline"
          className="sm:mt-6.5 max-lg:h-(--vs-control-touch)"
          pending={m.isPending}
        >
          {m.isPending ? null : <KeyRound aria-hidden />}
          {m.isPending ? "Creating" : "Create key"}
        </Button>
      </div>
      {m.error && Object.keys(serverErrors(m.error)).length === 0 ? (
        <FormMessage error={m.error} />
      ) : null}
    </form>
  );
}

export function CreatedKeyPanel({
  created,
  onDone,
}: {
  created: McpCredentialCreated;
  onDone: () => void;
}) {
  return (
    <section aria-label="New access key">
      <OneTimeSecret
        value={created.key}
        title={`Copy the key now: ${created.name}`}
        description={`It is shown only once; only its hash is stored. It expires ${
          created.expires_at ? formatDateTimeUtc(created.expires_at) : "never"
        }. The snippets above now hold it.`}
        onDone={onDone}
        copyLabel="Copy key"
      />
    </section>
  );
}

const STATUS: Record<McpCredential["status"], { tone: Tone; text: string }> = {
  active: { tone: "success", text: "Active" },
  expired: { tone: "info", text: "Expired" },
  revoked: { tone: "critical", text: "Revoked" },
};

export function CredentialList({
  items,
  canRevoke,
  revoke,
  invalidate,
}: {
  items: McpCredential[];
  canRevoke: (c: McpCredential) => boolean;
  revoke: (c: McpCredential) => Promise<void>;
  invalidate: () => void;
}) {
  if (items.length === 0)
    return (
      <div className="px-4 pb-4 sm:px-5">
        <StateBlock
          kind="empty"
          icon={<KeyRound aria-hidden strokeWidth={1.75} />}
          title="No key or connection yet."
        >
          Create an access key above, or connect a client through sign-in.
        </StateBlock>
      </div>
    );
  return (
    <Stagger
      as="ul"
      aria-label="MCP keys and connections"
      className="divide-y divide-line-subtle border-t border-line"
    >
      {items.map((c) => (
        <CredentialRow
          key={c.id}
          c={c}
          canRevoke={canRevoke(c)}
          revoke={() => revoke(c)}
          invalidate={invalidate}
        />
      ))}
    </Stagger>
  );
}

function CredentialRow({
  c,
  canRevoke,
  revoke,
  invalidate,
}: {
  c: McpCredential;
  canRevoke: boolean;
  revoke: () => Promise<void>;
  invalidate: () => void;
}) {
  const [confirm, setConfirm] = useState(false);
  const m = useMutation({
    mutationFn: revoke,
    onSuccess: () => {
      setConfirm(false);
      invalidate();
    },
  });
  const kind = c.kind === "key" ? "Access key" : "Connector";
  const st = STATUS[c.status];
  return (
    <StaggerItem
      as="li"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover sm:px-5"
      data-credential-id={c.id}
      aria-label={`${kind} ${c.name}`}
    >
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-caption text-fg-3">{kind}</span>
          <span className="min-w-0 truncate text-body font-medium text-fg">{c.name}</span>
          <Pill tone={st.tone}>{st.text}</Pill>
          {c.prefix ? (
            <code className="font-mono text-caption text-fg-3">{c.prefix}...</code>
          ) : null}
        </div>
        <p className="text-caption font-normal text-fg-3">
          {c.display_name ? `Acts as ${c.display_name}. ` : ""}Created{" "}
          {formatDateTimeUtc(c.created_at)}.{" "}
          {c.revoked_at
            ? `Revoked ${formatDateTimeUtc(c.revoked_at)}${c.revoked_reason ? `: ${REVOKED_REASON[c.revoked_reason] ?? c.revoked_reason}` : ""}.`
            : c.expires_at
              ? `${c.status === "expired" ? "Expired" : "Expires"} ${formatDateTimeUtc(c.expires_at)}.`
              : "Stays valid while the client refreshes it."}{" "}
          {c.last_seen_at ? `Last used ${formatDateTimeUtc(c.last_seen_at)}.` : "Never used."}
        </p>
      </div>
      {c.status === "active" && canRevoke ? (
        <Button
          variant="outline"
          size="sm"
          className="text-critical max-lg:h-(--vs-control-touch)"
          onClick={() => setConfirm(true)}
        >
          Revoke
        </Button>
      ) : null}
      <ConfirmDialog
        open={confirm}
        onOpenChange={(o) => {
          setConfirm(o);
          if (!o) m.reset();
        }}
        title={`Revoke ${c.name}?`}
        description="The next request of this client is refused. A revoked key or connection cannot be restored; make a new one instead."
        confirmLabel="Revoke"
        pendingLabel="Revoking"
        pending={m.isPending}
        error={m.error}
        onConfirm={() => m.mutate()}
      />
    </StaggerItem>
  );
}
