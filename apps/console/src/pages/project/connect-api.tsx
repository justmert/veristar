import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade, stagger } from "@veristar/brand/motion";
import { ArrowRight, Check, KeyRound, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type FormEvent, type ReactNode, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { keys, otlpTokensQuery } from "@/api/queries";
import type { OtlpToken, OtlpTokenCreated, Tenant } from "@/api/schemas";
import { CodeBlock, CopyField } from "@/components/code-block";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { TextField } from "@/components/form-field";
import { StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader, Panel } from "@/components/panel";
import { SecretPanel } from "@/components/secret-panel";
import { Pill, StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatDate, formatDateTimeUtc } from "@/lib/format";
import { inline } from "@/lib/markdown";
import { type OtelGuide, otelGuides, otlpEndpoint } from "@/lib/otel-guides";
import * as v from "@/lib/validate";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";

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

// the metrics writer stamps last_seen_at when data sent with a token is
// written (contract 6.1, at most once a minute), so the token list itself
// tells whether anything arrived yet. while nothing has, the list is asked
// again on this interval.
const FIRST_DATA_POLL = 30_000;

function waiting(tokens: OtlpToken[] | undefined): boolean {
  if (!tokens) return false;
  const active = tokens.filter((t) => t.status === "active");
  return active.length > 0 && active.every((t) => t.last_seen_at === null);
}

export function ConnectApiPage() {
  const { tenant, termsCurrent } = useProject();
  const endpoint = otlpEndpoint(window.location.hostname);
  const [showRevoked, setShowRevoked] = useState(false);
  const [created, setCreated] = useState<OtlpTokenCreated | null>(null);
  const list = useQuery({
    ...otlpTokensQuery(tenant.id, showRevoked),
    refetchInterval: (q) => (waiting(q.state.data) ? FIRST_DATA_POLL : false),
  });
  const tokens = list.data ?? [];
  const hasToken = tokens.some((t) => t.status === "active") || created !== null;
  const lastUsed = tokens
    .filter((t) => t.last_seen_at !== null)
    .sort((a, b) => Date.parse(b.last_seen_at ?? "") - Date.parse(a.last_seen_at ?? ""))[0];

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-4">
        <PageHeader
          title="Connect your API"
          description="Your app keeps exporting to your own backend; a second OpenTelemetry exporter sends a copy here with a token from this page. Only request counts, status codes and latency are kept."
        />
      </div>

      <div className="grid items-start gap-x-10 gap-y-6 xl:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <ol className="flex flex-col rounded-xl border border-line bg-raised">
            <Step n={1} state={hasToken ? "done" : "current"} title="Create an ingest token">
              <p className="max-w-prose text-small text-fg-2">
                A token identifies this project to the collector. Use one per app or environment, so
                a leaked token can be revoked without stopping the others. Data sent with a token
                always counts for this project, whatever the app puts in its resource attributes.
              </p>
              <AnimatePresence initial={false}>
                {created ? (
                  <motion.div key={created.id} {...fade}>
                    <CreatedToken token={created} onDone={() => setCreated(null)} />
                  </motion.div>
                ) : null}
              </AnimatePresence>
              <CreateGate tenant={tenant} canCreate={termsCurrent} onCreated={setCreated} />
            </Step>
            <Step
              n={2}
              state={hasToken ? (lastUsed ? "done" : "current") : "next"}
              title="Add a second exporter"
            >
              <EndpointSection endpoint={endpoint} />
              <GuidesSection />
            </Step>
            <Step n={3} state={lastUsed ? "done" : "next"} title="First data" last>
              <FirstData
                tenantId={tenant.id}
                hasToken={hasToken}
                lastUsed={lastUsed ?? null}
                loading={list.isPending}
              />
            </Step>
          </ol>

          <TokensSection
            tenantId={tenant.id}
            list={list}
            showRevoked={showRevoked}
            onShowRevoked={() => setShowRevoked((s) => !s)}
          />
        </div>
        <KeptSection />
      </div>
    </div>
  );
}

// a numbered step of the setup: done (a check), the current one (accent
// ring) or a later one (muted)
function Step({
  n,
  state,
  title,
  last = false,
  children,
}: {
  n: number;
  state: "done" | "current" | "next";
  title: string;
  last?: boolean;
  children: ReactNode;
}) {
  return (
    <li
      className={cn(
        "grid grid-cols-[1.75rem_minmax(0,1fr)] gap-3 p-4 sm:p-5",
        !last && "border-b border-line",
      )}
    >
      <span
        className={cn(
          "flex size-6 items-center justify-center rounded-full text-caption font-semibold transition-colors duration-(--vs-duration-change) ease-standard",
          state === "done"
            ? "bg-success text-ground"
            : state === "current"
              ? "border-2 border-accent-text text-accent-text"
              : "border-2 border-line-strong text-fg-3",
        )}
      >
        {state === "done" ? <Check aria-hidden className="size-3.5" strokeWidth={3} /> : n}
        <span className="sr-only">
          {state === "done" ? ", done" : state === "current" ? ", current step" : ""}
        </span>
      </span>
      <div className="flex min-w-0 flex-col gap-3">
        {/* the size stays outside cn: its merge treats a text size and a text
            colour as one group and keeps only the last */}
        <h2 className={`text-title ${state === "next" ? "text-fg-2" : "text-fg"}`}>{title}</h2>
        {children}
      </div>
    </li>
  );
}

function EndpointSection({ endpoint }: { endpoint: string | null }) {
  return (
    <section aria-labelledby="otlp-endpoint" className="flex flex-col gap-2">
      <h3 id="otlp-endpoint" className="text-small font-medium text-fg">
        Endpoint
      </h3>
      {endpoint ? (
        <CopyField value={endpoint} label="endpoint" className="max-w-md" />
      ) : (
        <p className="max-w-prose text-small text-fg-2">
          This console is not served from an app. host name, so the endpoint cannot be derived here.
          It is the console's host name with otlp. in place of app.
        </p>
      )}
      <p className="max-w-prose text-small text-fg-2">
        OTLP over HTTP (paths /v1/metrics, /v1/traces and /v1/logs) and gRPC, both on port 443. Send
        the token as the header{" "}
        <code className="font-mono text-fg">Authorization: Bearer sa_otlp_...</code> on the platform
        exporter only, never on the exporter to your own backend.
      </p>
    </section>
  );
}

function FirstData({
  tenantId,
  hasToken,
  lastUsed,
  loading,
}: {
  tenantId: string;
  hasToken: boolean;
  lastUsed: OtlpToken | null;
  loading: boolean;
}) {
  const state = loading ? "loading" : lastUsed ? "received" : hasToken ? "waiting" : "none";
  return (
    <AnimatePresence mode="wait" initial={false}>
      <motion.div key={state} {...fade} className="flex flex-col gap-2">
        {state === "received" && lastUsed ? (
          <>
            <p role="status" className="flex items-center gap-2 text-body text-fg">
              <StatusDot tone="success" />
              Data arrived. {lastUsed.name} was last used{" "}
              {formatDateTimeUtc(lastUsed.last_seen_at ?? "")}.
            </p>
            <Button asChild variant="outline" size="sm" className="self-start">
              <Link to={`/projects/${tenantId}/api`}>
                See the traffic
                <ArrowRight aria-hidden />
              </Link>
            </Button>
          </>
        ) : state === "waiting" ? (
          <>
            <p role="status" className="flex items-center gap-2 text-body text-fg">
              <StatusDot tone="info" live />
              Waiting for the first data sent with a token. This page checks every 30 seconds.
            </p>
            <p className="text-small text-fg-3">
              Exporters send in batches and a token's use is recorded at most once a minute, so it
              can take a minute or two to show.
            </p>
          </>
        ) : state === "none" ? (
          <p className="text-small text-fg-3">
            Once an app sends data with a token, this step shows when it arrived.
          </p>
        ) : null}
      </motion.div>
    </AnimatePresence>
  );
}

function TokensSection({
  tenantId,
  list,
  showRevoked,
  onShowRevoked,
}: {
  tenantId: string;
  list: ReturnType<typeof useQuery<OtlpToken[]>>;
  showRevoked: boolean;
  onShowRevoked: () => void;
}) {
  return (
    <Panel
      title="Tokens"
      description="Listed by prefix only; a token's full value is shown once, when it is created."
      actions={
        <label className="flex cursor-pointer items-center gap-2 text-small text-fg-2 max-lg:min-h-(--vs-control-touch)">
          <input
            type="checkbox"
            className="size-4 cursor-pointer accent-accent focus-ring"
            checked={showRevoked}
            onChange={onShowRevoked}
          />
          Show revoked tokens
        </label>
      }
      flush
    >
      {list.isPending ? (
        <div className="px-4 pb-4 sm:px-5">
          <Loading label="Loading tokens" rows={2} />
        </div>
      ) : null}
      {list.error ? (
        <div className="px-4 pb-4 sm:px-5">
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
        </div>
      ) : null}
      {list.data ? (
        list.data.length === 0 ? (
          <p className="mx-4 mb-4 animate-fade-in rounded-lg border border-dashed border-line-strong p-4 text-center text-small text-fg-2 sm:mx-5">
            {showRevoked ? "No tokens yet." : "No active tokens."}
          </p>
        ) : (
          <motion.ul
            aria-label="Tokens"
            initial="initial"
            animate="animate"
            variants={stagger}
            className="divide-y divide-line-subtle border-t border-line"
          >
            {list.data.map((t) => (
              <StaggerItem as="li" key={t.id}>
                <TokenRow tenantId={tenantId} token={t} />
              </StaggerItem>
            ))}
          </motion.ul>
        )
      ) : null}
    </Panel>
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
      <Notice tone="critical">
        New tokens cannot be created while the project is suspended. Existing tokens are refused by
        the collector until an operator resumes the project.
      </Notice>
    );
  if (tenant.status !== "active")
    return (
      <Notice tone="warning">
        Tokens can be created once operators have approved the project.
      </Notice>
    );
  if (!canCreate)
    return (
      <Notice tone="warning">
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
    <form noValidate onSubmit={submit} aria-label="Create a token" className="flex flex-col gap-3">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_12rem_auto] sm:items-start">
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
        <Button
          type="submit"
          className="max-lg:h-(--vs-control-touch) sm:mt-6"
          disabled={create.isPending}
          pending={create.isPending}
        >
          {create.isPending ? null : <KeyRound aria-hidden />}
          {create.isPending ? "Creating" : "Create token"}
        </Button>
      </div>
      <FormMessage
        error={
          create.error && Object.keys(serverTokenErrors(create.error)).length === 0
            ? create.error
            : null
        }
      />
    </form>
  );
}

// the plaintext lives only in this component's props, never in the query
// cache, and is gone once the panel is closed (contract 6.2: shown once)
export function CreatedToken({ token, onDone }: { token: OtlpTokenCreated; onDone: () => void }) {
  return (
    <SecretPanel
      value={token.token}
      title={`Token ${token.name} was created`}
      description="Copy it now and store it as a secret of your app. It is shown only this once; if it is lost, create a new one and revoke this one."
      onDone={onDone}
    />
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
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover sm:px-5">
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="flex flex-wrap items-center gap-2 text-body font-medium text-fg">
          <span className="truncate">{token.name}</span>
          {token.environment ? (
            <span className="rounded-full border border-line-strong px-2 font-mono text-axis text-fg-2">
              {token.environment}
            </span>
          ) : null}
          {active ? null : <Pill tone="info">Revoked</Pill>}
        </p>
        <p className="text-small text-fg-2">
          <code className="font-mono text-fg">{token.prefix}...</code>, created{" "}
          {formatDate(token.created_at)}
          {token.revoked_at ? `, revoked ${formatDateTimeUtc(token.revoked_at)}` : ""}
          {token.last_seen_at
            ? `, last used ${formatDateTimeUtc(token.last_seen_at)}`
            : ", not used yet"}
        </p>
      </div>
      {active ? (
        <>
          <Button
            variant="outline"
            size="sm"
            className="max-lg:h-(--vs-control-touch)"
            onClick={() => {
              revoke.reset();
              setOpen(true);
            }}
          >
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

function Kept({ yes, children }: { yes: boolean; children: ReactNode }) {
  return (
    <li className="flex gap-2 border-t border-line py-2 text-small text-fg-2">
      {yes ? (
        <Check aria-hidden className="mt-1 size-3.5 shrink-0 text-success" strokeWidth={2.6} />
      ) : (
        <X aria-hidden className="mt-1 size-3.5 shrink-0 text-fg-3" strokeWidth={2.6} />
      )}
      <span>{children}</span>
    </li>
  );
}

function KeptSection() {
  return (
    <aside aria-labelledby="otlp-kept" className="flex flex-col xl:sticky xl:top-6">
      <h2 id="otlp-kept" className="pb-2 text-caption text-fg-3">
        What this service keeps
      </h2>
      <p className="border-t border-line py-2 text-small text-fg">
        Only HTTP server request duration is kept: the metric http.server.request.duration (the
        older http.server.duration in milliseconds is converted to seconds), with the method, route,
        status code and error type, per service.name.
      </p>
      <ul>
        <Kept yes={false}>
          Every other metric is dropped when it arrives, together with every other attribute.
        </Kept>
        <Kept yes={false}>
          Traces are not stored. When an app sends no metrics, durations are counted from its server
          spans, and then the spans are discarded with their names, events, links and attributes.
          Sampled traces count fewer requests than metrics do.
        </Kept>
        <Kept yes={false}>Logs are accepted, so your SDK reports no errors, and discarded.</Kept>
        <Kept yes>
          Routes are normalized: ids, Stellar account and contract keys, hashes and similar values
          become placeholders such as {"{id}"} or {"{account}"}, and a secret seed in a path becomes{" "}
          {"{redacted-secret}"}.
        </Kept>
        <Kept yes>
          Exponential histograms are re-bucketed onto fixed bucket bounds and marked as approximate.
        </Kept>
      </ul>
      <p className="border-t border-line py-2 text-small text-fg-2">
        Your own backend keeps the full data. The exporter to it is unchanged and receives every
        metric, trace and log as before; only the second exporter, the one with the token, sends
        here.
      </p>
    </aside>
  );
}

function GuidesSection() {
  const guides = otelGuides();
  return (
    <section aria-labelledby="otlp-guides" className="flex flex-col gap-3">
      <h3 id="otlp-guides" className="text-small font-medium text-fg">
        Set up your app
      </h3>
      <p className="max-w-prose text-small text-fg-2">
        The code below is taken unchanged from the example apps in the repository, which the
        service's integration tests run against the real collector. Each app exports both signals
        twice: to your own backend without a token, and here with it. Set service.name so each
        service shows up separately.
      </p>
      <Tabs defaultValue={guides[0]?.id ?? "node"} className="gap-3">
        <TabsList className="max-w-full overflow-x-auto">
          {guides.map((g) => (
            <TabsTrigger key={g.id} value={g.id} className="max-lg:h-(--vs-control-touch)">
              {g.label}
            </TabsTrigger>
          ))}
        </TabsList>
        {guides.map((g) => (
          <TabsContent key={g.id} value={g.id} className="animate-rise-in">
            <Guide guide={g} />
          </TabsContent>
        ))}
      </Tabs>
    </section>
  );
}

function Guide({ guide }: { guide: OtelGuide }) {
  return (
    <div className="flex flex-col gap-5">
      <div className="flex max-w-prose flex-col gap-2 text-small text-fg-2">
        {guide.prose.map((b) =>
          b.kind === "p" ? (
            <p key={b.text}>{inline(b.text)}</p>
          ) : (
            <pre
              key={b.text}
              className="overflow-x-auto rounded-md border border-line bg-sunken px-3 py-2 font-mono text-caption font-normal whitespace-pre-wrap text-fg"
            >
              <code>{b.text}</code>
            </pre>
          ),
        )}
        <p className="text-fg-3">
          Full example: <code className="font-mono text-fg-2">{guide.dir}</code>
        </p>
      </div>

      {guide.files.map((f) => (
        <CodeBlock
          key={`${f.path}:${f.caption}`}
          code={f.code}
          className="[&_pre]:max-h-[32rem]"
          label={
            <>
              <code className="font-mono text-fg">{f.path}</code>
              <span>: {f.caption}</span>
            </>
          }
        />
      ))}

      <div className="flex flex-col gap-2">
        <h4 className="text-small font-medium text-fg">Environment of the example</h4>
        <div className="overflow-x-auto rounded-lg border border-line">
          <table className="w-full text-left text-small">
            <thead className="text-caption text-fg-2">
              <tr className="border-b border-line">
                <th scope="col" className="h-9 px-3 font-medium">
                  Variable
                </th>
                <th scope="col" className="px-3 font-medium">
                  Values
                </th>
                <th scope="col" className="px-3 font-medium">
                  Purpose
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle text-fg-2">
              {guide.env.map((r) => (
                <tr key={r.name}>
                  <td className="px-3 py-2 font-mono text-caption font-normal whitespace-nowrap text-fg">
                    {r.name}
                  </td>
                  <td className="px-3 py-2">{inline(r.values)}</td>
                  <td className="px-3 py-2">{inline(r.purpose)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="max-w-prose text-small text-fg-3">{inline(guide.envNote)}</p>
      </div>
    </div>
  );
}
