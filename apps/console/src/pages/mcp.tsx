import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
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
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDateTimeUtc } from "@/lib/format";
import { Notice, useProject } from "./project-layout";

// the project's "AI / MCP" page (contract 12.8, docs/onboarding/mcp.mdx):
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
    <div className="space-y-8">
      <header>
        <h2 className="text-xl font-semibold tracking-tight">AI assistants (MCP)</h2>
        <p className="mt-1 max-w-prose text-muted-foreground">
          Ask Claude, ChatGPT, Cursor or any client of the Model Context Protocol about this
          project's web and API analytics. Every tool is read-only. A connection acts as the person
          who made it and reads this project only; it stops working when that person leaves the
          project, and when the project is suspended or removed.
        </p>
      </header>

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
        <Notice tone="warn">
          <p className="font-medium">No new keys while the project is {tenant.status}</p>
        </Notice>
      )}

      <section aria-labelledby="mcp-list" className="space-y-3">
        <h3 id="mcp-list" className="font-semibold">
          Keys and connections
        </h3>
        <p className="max-w-prose text-sm text-muted-foreground">
          Access keys made on this page and connectors approved through sign-in. Members can revoke
          their own; owners can revoke any.
        </p>
        {list.isPending ? <Loading label="Loading keys" /> : null}
        {list.error ? <ErrorPanel error={list.error} onRetry={() => list.refetch()} /> : null}
        {list.data ? (
          <CredentialList
            items={list.data}
            canRevoke={(c) => isOwner || c.user_id === me.user.id}
            revoke={(c) => mcpApi.revokeTenantCredential(tenant.id, c.id)}
            invalidate={() => qc.invalidateQueries({ queryKey: mcpKeys.tenant(tenant.id) })}
          />
        ) : null}
      </section>
    </div>
  );
}

// how to connect each client. with a key just made, the snippets hold it.
export function Connect({ created }: { created: McpCredentialCreated | null }) {
  const origin = window.location.origin;
  const s = clientSnippets(origin, created?.key ?? null);
  return (
    <section
      aria-labelledby="mcp-connect"
      className="space-y-3 rounded-xl border bg-card p-4 sm:p-5"
    >
      <h3 id="mcp-connect" className="font-semibold">
        Connect a client
      </h3>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="text-muted-foreground">Server URL</span>
        <code className="rounded-md border bg-muted px-2 py-1 font-mono text-xs break-all">
          {mcpEndpoint(origin)}
        </code>
        <CopyButton text={mcpEndpoint(origin)} label="Copy URL" />
      </div>
      <Tabs defaultValue="connector">
        <TabsList className="flex-wrap">
          <TabsTrigger value="connector">Claude and ChatGPT</TabsTrigger>
          <TabsTrigger value="claude-code">Claude Code</TabsTrigger>
          <TabsTrigger value="cursor">Cursor</TabsTrigger>
        </TabsList>
        <TabsContent value="connector" className="space-y-2 pt-3 text-sm">
          <p className="max-w-prose text-muted-foreground">
            In claude.ai or Claude Desktop open Settings, Connectors, Add custom connector; in
            ChatGPT open Settings, Connectors, Create. Paste the server URL and leave the client
            credentials empty. The client opens this console, you sign in with GitHub, choose the
            project and approve. No key is needed.
          </p>
          <Snippet text={s.connectorUrl} label="Copy connector URL" />
        </TabsContent>
        <TabsContent value="claude-code" className="space-y-2 pt-3 text-sm">
          <p className="text-muted-foreground">With an access key from this page:</p>
          <Snippet text={s.claudeCode} label="Copy command" />
          <p className="text-muted-foreground">Or sign in through the browser instead of a key:</p>
          <Snippet text={s.claudeCodeOAuth} label="Copy command" />
        </TabsContent>
        <TabsContent value="cursor" className="space-y-2 pt-3 text-sm">
          <p className="text-muted-foreground">
            Add to <code className="font-mono">.cursor/mcp.json</code> or the global{" "}
            <code className="font-mono">~/.cursor/mcp.json</code>:
          </p>
          <Snippet text={s.cursor} label="Copy configuration" />
        </TabsContent>
      </Tabs>
      {created ? null : (
        <p className="text-xs text-muted-foreground">
          Create a key below and the snippets fill it in.
        </p>
      )}
    </section>
  );
}

function Snippet({ text, label }: { text: string; label: string }) {
  return (
    <div className="space-y-2">
      <pre className="overflow-x-auto rounded-lg border bg-muted p-3 font-mono text-xs">{text}</pre>
      <CopyButton text={text} label={label} />
    </div>
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
      className="space-y-4 rounded-xl border bg-card p-4 sm:p-5"
    >
      <h3 className="font-semibold">{label}</h3>
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField
          label="Name"
          hint="Where the key is used, for example laptop or Cursor."
          maxLength={100}
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
          value={form.days}
          error={errors.days}
          onChange={(e) => setForm({ ...form, days: e.target.value })}
        />
      </div>
      {m.error && Object.keys(serverErrors(m.error)).length === 0 ? (
        <FormMessage error={m.error} />
      ) : null}
      <Button type="submit" disabled={m.isPending}>
        <KeyRound aria-hidden />
        {m.isPending ? "Creating" : "Create key"}
      </Button>
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
    <section
      aria-label="New access key"
      className="space-y-3 rounded-xl border border-primary/40 bg-card p-4 sm:p-5"
    >
      <h3 className="font-semibold">Key {created.name} created</h3>
      <p className="text-sm text-muted-foreground">
        Copy the key now. It is shown only once; only its hash is stored. It expires{" "}
        {created.expires_at ? formatDateTimeUtc(created.expires_at) : "never"}. The snippets above
        now hold it.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <code
          data-testid="new-mcp-key"
          className="min-w-0 rounded-lg border bg-muted px-3 py-2 font-mono text-sm break-all"
        >
          {created.key}
        </code>
        <CopyButton text={created.key} label="Copy key" />
      </div>
      <Button variant="outline" onClick={onDone}>
        I have copied the key
      </Button>
    </section>
  );
}

const STATUS_STYLE: Record<McpCredential["status"], string> = {
  active: "border-good/40 bg-good/10",
  expired: "bg-muted text-muted-foreground",
  revoked: "border-destructive/40 bg-destructive/5",
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
      <p className="rounded-xl border border-dashed bg-card p-6 text-muted-foreground">
        No key or connection yet.
      </p>
    );
  return (
    <ul
      aria-label="MCP keys and connections"
      className="divide-y overflow-hidden rounded-xl border bg-card"
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
    </ul>
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
  return (
    <li
      className="space-y-2 px-4 py-4 sm:px-5"
      data-credential-id={c.id}
      aria-label={`${kind} ${c.name}`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <p className="font-medium">{c.name}</p>
        <span className="rounded-full border px-2 py-0.5 text-xs">{kind}</span>
        <span
          className={`rounded-full border px-2 py-0.5 text-xs font-medium ${STATUS_STYLE[c.status]}`}
        >
          {c.status === "active" ? "Active" : c.status === "expired" ? "Expired" : "Revoked"}
        </span>
        {c.prefix ? (
          <code className="font-mono text-xs text-muted-foreground">{c.prefix}...</code>
        ) : null}
      </div>
      <p className="text-xs text-muted-foreground">
        {c.github_login ? `Acts as ${c.github_login}. ` : ""}Created{" "}
        {formatDateTimeUtc(c.created_at)}.{" "}
        {c.revoked_at
          ? `Revoked ${formatDateTimeUtc(c.revoked_at)}${c.revoked_reason ? `: ${REVOKED_REASON[c.revoked_reason] ?? c.revoked_reason}` : ""}.`
          : c.expires_at
            ? `${c.status === "expired" ? "Expired" : "Expires"} ${formatDateTimeUtc(c.expires_at)}.`
            : "Stays valid while the client refreshes it."}{" "}
        {c.last_seen_at ? `Last used ${formatDateTimeUtc(c.last_seen_at)}.` : "Never used."}
      </p>
      {c.status === "active" && canRevoke ? (
        <Button
          variant="outline"
          size="sm"
          className="text-destructive"
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
    </li>
  );
}
