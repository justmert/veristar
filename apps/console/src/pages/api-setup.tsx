import { useMutation, useQuery } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { type FormEvent, useState } from "react";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, otlpTokensQuery } from "@/api/queries";
import type { OtlpToken, OtlpTokenCreated, Tenant } from "@/api/schemas";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDate, formatDateTimeUtc } from "@/lib/format";
import { inline } from "@/lib/markdown";
import { type OtelGuide, otelGuides, otlpEndpoint } from "@/lib/otel-guides";
import * as v from "@/lib/validate";
import { queryClient } from "@/query-client";
import { Notice, useProject } from "./project-layout";

export type TokenValues = { name: string; environment: string };
export type TokenErrors = Partial<Record<keyof TokenValues, string>>;

// contract 6.2: environment is empty or a short lowercase label
const ENVIRONMENT = /^[a-z0-9]([a-z0-9._-]{0,31})?$/;

export function validateToken(values: TokenValues): TokenErrors {
  const out: TokenErrors = {};
  const name = v.siteName(values.name);
  if (name) out.name = v.fieldMessage(name);
  const env = values.environment.trim();
  if (env !== "" && !ENVIRONMENT.test(env)) {
    out.environment = "Use up to 32 lowercase letters, digits, dots, dashes or underscores.";
  }
  return out;
}

export function serverTokenErrors(err: unknown): TokenErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: TokenErrors = {};
  for (const [path, code] of Object.entries(err.fields)) {
    if (path === "environment") out.environment = v.fieldMessage(code);
    else if (code === "too_many")
      out.name = "This project has 50 active tokens. Revoke one before creating another.";
    else out.name = v.fieldMessage(code);
  }
  return out;
}

export function ApiSetupPage() {
  const { tenant, termsCurrent } = useProject();
  const endpoint = otlpEndpoint(window.location.hostname);
  return (
    <div className="space-y-10">
      <header>
        <h2 className="text-xl font-semibold tracking-tight">API analytics setup</h2>
        <p className="mt-1 max-w-prose text-muted-foreground">
          Request counts, latency and errors of your HTTP API, sent with OpenTelemetry. Your app
          keeps exporting to your own backend; a second exporter sends a copy here with a token from
          this page.
        </p>
      </header>

      <EndpointSection endpoint={endpoint} />
      <TokensSection tenant={tenant} canCreate={termsCurrent} />
      <KeptSection />
      <GuidesSection />
    </div>
  );
}

function EndpointSection({ endpoint }: { endpoint: string | null }) {
  return (
    <section aria-labelledby="otlp-endpoint" className="space-y-3">
      <h3 id="otlp-endpoint" className="text-lg font-semibold tracking-tight">
        Endpoint
      </h3>
      {endpoint ? (
        <div className="flex flex-wrap items-center gap-2">
          <code className="rounded-md border bg-canvas px-2 py-1 font-mono text-sm break-all">
            {endpoint}
          </code>
          <CopyButton text={endpoint} label="Copy endpoint" />
        </div>
      ) : (
        <p className="max-w-prose text-sm text-muted-foreground">
          This console is not served from an app. host name, so the endpoint cannot be derived here.
          It is the console's host name with otlp. in place of app.
        </p>
      )}
      <p className="max-w-prose text-sm text-muted-foreground">
        OTLP over HTTP (paths /v1/metrics, /v1/traces and /v1/logs) and gRPC, both on port 443. Send
        the token as the header <code className="font-mono">Authorization: Bearer sa_otlp_...</code>{" "}
        on the platform exporter only, never on the exporter to your own backend.
      </p>
    </section>
  );
}

function TokensSection({ tenant, canCreate }: { tenant: Tenant; canCreate: boolean }) {
  const [showRevoked, setShowRevoked] = useState(false);
  const [created, setCreated] = useState<OtlpTokenCreated | null>(null);
  const list = useQuery(otlpTokensQuery(tenant.id, showRevoked));

  return (
    <section aria-labelledby="otlp-tokens" className="space-y-4">
      <h3 id="otlp-tokens" className="text-lg font-semibold tracking-tight">
        Tokens
      </h3>
      <p className="max-w-prose text-sm text-muted-foreground">
        A token identifies this project to the collector. Use one per app or environment, so a
        leaked token can be revoked without stopping the others. Data sent with a token always
        counts for this project, whatever the app puts in its resource attributes.
      </p>

      {created ? <CreatedToken token={created} onDone={() => setCreated(null)} /> : null}

      <CreateGate tenant={tenant} canCreate={canCreate} onCreated={setCreated} />

      <label className="flex w-fit items-center gap-1.5 text-sm">
        <input
          type="checkbox"
          className="size-4 accent-primary"
          checked={showRevoked}
          onChange={() => setShowRevoked((s) => !s)}
        />
        Show revoked tokens
      </label>

      {list.isPending ? <Loading label="Loading tokens" /> : null}
      {list.error ? <ErrorPanel error={list.error} onRetry={() => list.refetch()} /> : null}
      {list.data ? (
        list.data.length === 0 ? (
          <p className="rounded-xl border border-dashed p-4 text-sm text-muted-foreground">
            {showRevoked ? "No tokens yet." : "No active tokens."}
          </p>
        ) : (
          <ul aria-label="Tokens" className="divide-y overflow-hidden rounded-xl border bg-card">
            {list.data.map((t) => (
              <li key={t.id}>
                <TokenRow tenantId={tenant.id} token={t} />
              </li>
            ))}
          </ul>
        )
      ) : null}
    </section>
  );
}

// the create form only where the control plane would accept it (contract
// 6.6); otherwise the reason, so nobody fills in a form that must fail
function CreateGate({
  tenant,
  canCreate,
  onCreated,
}: {
  tenant: Tenant;
  canCreate: boolean;
  onCreated: (t: OtlpTokenCreated) => void;
}) {
  if (tenant.status === "suspended")
    return (
      <Notice tone="bad">
        New tokens cannot be created while the project is suspended. Existing tokens are refused by
        the collector until an operator resumes the project.
      </Notice>
    );
  if (tenant.status !== "active")
    return (
      <Notice tone="warn">Tokens can be created once operators have approved the project.</Notice>
    );
  if (!canCreate)
    return (
      <Notice tone="warn">
        New tokens can be created once an owner accepts the current terms. Revoking works now.
      </Notice>
    );
  return <CreateTokenForm tenantId={tenant.id} onCreated={onCreated} />;
}

export function CreateTokenForm({
  tenantId,
  onCreated,
}: {
  tenantId: string;
  onCreated: (t: OtlpTokenCreated) => void;
}) {
  const [values, setValues] = useState<TokenValues>({ name: "", environment: "" });
  const [errors, setErrors] = useState<TokenErrors>({});
  const create = useMutation({
    mutationFn: (body: { name: string; environment?: string }) =>
      api.createOtlpToken(tenantId, body),
    onSuccess: async (t) => {
      setValues({ name: "", environment: "" });
      onCreated(t);
      await queryClient.invalidateQueries({ queryKey: keys.otlpTokensAll(tenantId) });
    },
    onError: (err) => setErrors(serverTokenErrors(err)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateToken(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    const env = values.environment.trim();
    create.mutate({ name: values.name.trim(), ...(env ? { environment: env } : {}) });
  }

  return (
    <form
      noValidate
      onSubmit={submit}
      aria-label="Create a token"
      className="rounded-xl border bg-card p-4 sm:p-5"
    >
      <div className="grid gap-4 sm:grid-cols-[1fr_12rem_auto] sm:items-start">
        <TextField
          label="Token name"
          name="name"
          autoComplete="off"
          maxLength={100}
          placeholder="Payments API"
          value={values.name}
          onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
          error={errors.name}
        />
        <TextField
          label="Environment"
          name="environment"
          optional
          autoCapitalize="none"
          autoComplete="off"
          spellCheck={false}
          maxLength={32}
          placeholder="production"
          value={values.environment}
          onChange={(e) => setValues((s) => ({ ...s, environment: e.target.value }))}
          error={errors.environment}
        />
        <Button type="submit" className="sm:mt-6" disabled={create.isPending}>
          <KeyRound aria-hidden />
          {create.isPending ? "Creating" : "Create token"}
        </Button>
      </div>
      <div className="mt-3">
        <FormMessage
          error={
            create.error && Object.keys(serverTokenErrors(create.error)).length === 0
              ? create.error
              : null
          }
        />
      </div>
    </form>
  );
}

// the plaintext lives only in this component's props, never in the query
// cache, and is gone once the panel is closed (contract 6.2: shown once)
export function CreatedToken({ token, onDone }: { token: OtlpTokenCreated; onDone: () => void }) {
  return (
    <section
      aria-label="New token"
      className="space-y-3 rounded-xl border border-good/40 bg-good/5 p-4 text-sm sm:p-5"
    >
      <p className="font-medium">Token {token.name} was created</p>
      <p className="text-muted-foreground">
        Copy it now and store it as a secret of your app. It is shown only this once; if it is lost,
        create a new one and revoke this one.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <code
          data-testid="new-token"
          className="rounded-md border bg-card px-2 py-1 font-mono text-sm break-all"
        >
          {token.token}
        </code>
        <CopyButton text={token.token} label="Copy token" />
      </div>
      <Button variant="outline" size="sm" onClick={onDone}>
        I stored the token
      </Button>
    </section>
  );
}

function TokenRow({ tenantId, token }: { tenantId: string; token: OtlpToken }) {
  const [open, setOpen] = useState(false);
  const revoke = useMutation({
    mutationFn: () => api.revokeOtlpToken(tenantId, token.id),
    onSuccess: async () => {
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: keys.otlpTokensAll(tenantId) });
    },
  });
  const active = token.status === "active";
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 p-4 sm:px-5">
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2 font-medium">
          <span className="truncate">{token.name}</span>
          {token.environment ? (
            <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-normal text-muted-foreground">
              {token.environment}
            </span>
          ) : null}
          {active ? null : (
            <span className="rounded-full bg-destructive/10 px-2 py-0.5 text-xs font-medium text-destructive">
              Revoked
            </span>
          )}
        </p>
        <p className="text-sm text-muted-foreground">
          <code className="font-mono">{token.prefix}...</code>, created{" "}
          {formatDate(token.created_at)}
          {token.revoked_at ? `, revoked ${formatDateTimeUtc(token.revoked_at)}` : ""}
          {token.last_seen_at
            ? `, last used ${formatDateTimeUtc(token.last_seen_at)}`
            : ", not used yet"}
        </p>
      </div>
      {active ? (
        <>
          <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
            Revoke
          </Button>
          <ConfirmDialog
            open={open}
            onOpenChange={setOpen}
            title={`Revoke ${token.name}?`}
            description="The collector refuses this token within seconds. Apps still sending with it get 401 until they use a new token. This cannot be undone."
            confirmLabel="Revoke token"
            pendingLabel="Revoking"
            pending={revoke.isPending}
            error={revoke.error}
            onConfirm={() => revoke.mutate()}
          />
        </>
      ) : null}
    </div>
  );
}

function KeptSection() {
  return (
    <section aria-labelledby="otlp-kept" className="space-y-3">
      <h3 id="otlp-kept" className="text-lg font-semibold tracking-tight">
        What this service keeps
      </h3>
      <div className="max-w-prose space-y-3 rounded-xl border bg-card p-4 text-sm sm:p-5">
        <p>
          Only HTTP server request duration is kept: the metric http.server.request.duration (the
          older http.server.duration in milliseconds is converted to seconds), with the method,
          route, status code and error type, per service.name.
        </p>
        <ul className="list-disc space-y-1.5 pl-5 text-muted-foreground">
          <li>
            Every other metric is dropped when it arrives, together with every other attribute.
          </li>
          <li>
            Traces are not stored. When an app sends no metrics, durations are counted from its
            server spans, and then the spans are discarded with their names, events, links and
            attributes. Sampled traces count fewer requests than metrics do.
          </li>
          <li>Logs are accepted, so your SDK reports no errors, and discarded.</li>
          <li>
            Routes are normalized: ids, Stellar account and contract keys, hashes and similar values
            become placeholders such as {"{id}"} or {"{account}"}, and a secret seed in a path
            becomes {"{redacted-secret}"}.
          </li>
          <li>
            Exponential histograms are re-bucketed onto fixed bucket bounds and marked as
            approximate.
          </li>
        </ul>
        <p>
          Your own backend keeps the full data. The exporter to it is unchanged and receives every
          metric, trace and log as before; only the second exporter, the one with the token, sends
          here.
        </p>
      </div>
    </section>
  );
}

function GuidesSection() {
  const guides = otelGuides();
  return (
    <section aria-labelledby="otlp-guides" className="space-y-3">
      <h3 id="otlp-guides" className="text-lg font-semibold tracking-tight">
        Set up your app
      </h3>
      <p className="max-w-prose text-sm text-muted-foreground">
        The code below is taken unchanged from the example apps in the repository, which the
        service's integration tests run against the real collector. Each app exports both signals
        twice: to your own backend without a token, and here with it.
      </p>
      <Tabs defaultValue={guides[0]?.id ?? "node"} className="gap-3">
        <TabsList className="h-auto max-w-full justify-start overflow-x-auto">
          {guides.map((g) => (
            <TabsTrigger key={g.id} value={g.id}>
              {g.label}
            </TabsTrigger>
          ))}
        </TabsList>
        {guides.map((g) => (
          <TabsContent key={g.id} value={g.id}>
            <Guide guide={g} />
          </TabsContent>
        ))}
      </Tabs>
    </section>
  );
}

function Guide({ guide }: { guide: OtelGuide }) {
  return (
    <div className="space-y-5">
      <div className="max-w-prose space-y-2 text-sm">
        {guide.prose.map((b) =>
          b.kind === "p" ? (
            <p key={b.text}>{inline(b.text)}</p>
          ) : (
            <pre
              key={b.text}
              className="rounded-lg border bg-canvas p-3 font-mono text-xs whitespace-pre-wrap"
            >
              <code>{b.text}</code>
            </pre>
          ),
        )}
        <p className="text-muted-foreground">
          Full example: <code className="font-mono">{guide.dir}</code>
        </p>
      </div>

      {guide.files.map((f) => (
        <figure key={`${f.path}:${f.caption}`} className="space-y-2">
          <figcaption className="flex flex-wrap items-center justify-between gap-2 text-sm">
            <span>
              <code className="font-mono font-medium">{f.path}</code>
              <span className="text-muted-foreground">: {f.caption}</span>
            </span>
            <CopyButton text={f.code} label="Copy" />
          </figcaption>
          <pre className="max-h-[32rem] overflow-auto rounded-lg border bg-canvas p-4 font-mono text-xs leading-relaxed">
            <code>{f.code}</code>
          </pre>
        </figure>
      ))}

      <div className="space-y-2">
        <h4 className="text-sm font-semibold">Environment of the example</h4>
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-left text-sm">
            <thead className="bg-muted/50 text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="px-3 py-2 font-medium">
                  Variable
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  Values
                </th>
                <th scope="col" className="px-3 py-2 font-medium">
                  Purpose
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {guide.env.map((r) => (
                <tr key={r.name}>
                  <td className="px-3 py-2 font-mono text-xs whitespace-nowrap">{r.name}</td>
                  <td className="px-3 py-2">{inline(r.values)}</td>
                  <td className="px-3 py-2">{inline(r.purpose)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="max-w-prose text-sm text-muted-foreground">{inline(guide.envNote)}</p>
      </div>
    </div>
  );
}
