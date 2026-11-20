import { useMutation, useQuery } from "@tanstack/react-query";
import { Mark } from "@veristar/brand/mark";
import { Check, Lock, X } from "lucide-react";
import { useState } from "react";
import { ApiError } from "@/api/client";
import { type ConsentRequest, mcpApi, mcpKeys } from "@/api/mcp";
import { meQuery } from "@/api/queries";
import { ChoiceCard } from "@/components/choice-card";
import { FormMessage } from "@/components/error-panel";
import { Notice } from "@/components/notice";
import { PublicFrame } from "@/components/public-frame";
import { ErrorState, LoadingState, MessagePage, Skeleton } from "@/components/states";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/errors";
import { ProjectsButton } from "./not-found";

// the oauth consent screen of the mcp authorization flow (contract 12.3).
// /oauth/authorize checks the client and sends the browser here with a
// sealed request; the person signs in with github, picks the project (or
// the operator view) and approves, and the page then follows the redirect
// back to the ai client. nothing is granted without the approve click.

const STORE = "veristar.mcp.consent";

// errors /oauth/authorize can send before it trusts the client
const AUTHORIZE_ERRORS: Record<string, string> = {
  invalid_client:
    "The AI client is not registered here. Remove the connector in the client and add it again.",
  invalid_request: "The AI client sent an authorization request this server cannot accept.",
};

// the sealed request can be longer than a sign-in return path may be, so it
// moves to session storage and the address bar keeps the short path; after
// the github sign-in the page finds it there
function takeRequest(): string | null {
  const params = new URLSearchParams(window.location.search);
  const fromUrl = params.get("request");
  try {
    if (fromUrl) {
      sessionStorage.setItem(STORE, fromUrl);
      window.history.replaceState(null, "", "/ai/consent");
      return fromUrl;
    }
    return sessionStorage.getItem(STORE);
  } catch {
    // storage can be blocked; the request from the address bar still works
    // for a person who is already signed in
    return fromUrl;
  }
}

function forget() {
  try {
    sessionStorage.removeItem(STORE);
  } catch {
    // nothing stored
  }
}

export function AiConsentPage() {
  const [params] = useState(() => new URLSearchParams(window.location.search));
  const error = params.get("error");
  const [req] = useState(() => (error ? null : takeRequest()));

  return (
    // the same frame and place as every other full-page message; only the
    // consent form itself is narrower
    <PublicFrame>
      {error ? (
        <Stop tone="critical" code="AI connection" title="The connection was not set up">
          <p role="alert">
            {AUTHORIZE_ERRORS[error] ??
              params.get("error_description") ??
              "The AI client sent a request this server cannot accept. Start again from the AI client."}
          </p>
        </Stop>
      ) : req ? (
        <Signed request={req} />
      ) : (
        <Stop tone="info" code="AI connection" title="No connection request">
          <p>
            Start from your AI client: add the Veristar server address as a connector, and the
            client opens this page.
          </p>
        </Stop>
      )}
    </PublicFrame>
  );
}

// a page that explains why there is nothing to approve, with the way back
function Stop({
  tone,
  code,
  title,
  children,
}: {
  tone: "info" | "critical";
  code: string;
  title: string;
  children?: React.ReactNode;
}) {
  return (
    <MessagePage
      code={code}
      tone={tone === "critical" ? "critical" : "neutral"}
      title={title}
      actions={<ProjectsButton />}
      className="w-full"
    >
      {children}
    </MessagePage>
  );
}

// rendered for a request; /api/me answers 401 without a session, and the
// query client then sends the browser to sign-in and back to /ai/consent
function Signed({ request }: { request: string }) {
  const me = useQuery(meQuery);
  const inspect = useQuery({
    queryKey: mcpKeys.consent(request),
    queryFn: () => mcpApi.inspect(request),
    enabled: me.isSuccess,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  if (me.isPending || (me.isSuccess && inspect.isPending))
    return (
      <LoadingState label="Loading the request" className="w-full max-w-dialog-sm">
        <Skeleton.Table rows={3} columns={1} />
      </LoadingState>
    );
  if (me.error)
    return (
      <ErrorState
        title="Your account did not load"
        error={me.error}
        onRetry={() => me.refetch()}
        className="w-full max-w-prose"
      />
    );
  if (inspect.error)
    return (
      <Stop tone="critical" code="AI connection" title="This request cannot be approved">
        <p role="alert">{inspectErrorText(inspect.error)}</p>
        <p>A request expires after 10 minutes. Start again from the AI client.</p>
      </Stop>
    );
  if (!inspect.data) return null;
  return (
    <div className="w-full max-w-140">
      <Consent request={request} info={inspect.data} login={me.data?.user.display_name ?? ""} />
    </div>
  );
}

// an expired or changed sealed request is a validation error of the whole
// request, not of a field: say what it means on this page
function inspectErrorText(err: unknown): string {
  if (err instanceof ApiError && err.code === "validation_failed")
    return "The request expired or is not complete.";
  return errorMessage(err);
}

function Consent({
  request,
  info,
  login,
}: {
  request: string;
  info: ConsentRequest;
  login: string;
}) {
  const first = info.tenants[0]?.id ?? (info.operator ? "operator" : "");
  const [choice, setChoice] = useState<string>(first);
  const go = (to: string) => {
    forget();
    window.location.assign(to);
  };
  const approve = useMutation({
    mutationFn: () =>
      choice === "operator"
        ? mcpApi.approve(request, "operator", null)
        : mcpApi.approve(request, "tenant", choice),
    onSuccess: (r) => go(r.redirect_to),
  });
  const deny = useMutation({
    mutationFn: () => mcpApi.deny(request),
    onSuccess: (r) => go(r.redirect_to),
  });
  const busy = approve.isPending || deny.isPending || approve.isSuccess || deny.isSuccess;
  const nothing = info.tenants.length === 0 && !info.operator;
  const operator = choice === "operator";

  return (
    <form
      aria-label="Approve the connection"
      className="flex flex-col gap-6"
      onSubmit={(e) => {
        e.preventDefault();
        if (choice) approve.mutate();
      }}
    >
      <Ends client={info.client_name} />
      <div className="flex flex-col gap-2 text-center">
        <h1 className="text-page leading-8 font-semibold tracking-tight text-fg">
          {operator
            ? `Let ${info.client_name} read every project`
            : `Let ${info.client_name} read one project`}
        </h1>
        <p className="text-body text-fg-2">
          Signed in as <span className="font-semibold text-fg">{login}</span>. It reads as you.
        </p>
      </div>
      {nothing ? (
        <Notice tone="warning" role="alert" title="Nothing to connect">
          You are not a member of a project that can be connected. Join or enroll a project first.
        </Notice>
      ) : (
        <fieldset className="flex flex-col gap-2">
          <legend className="sr-only">Give access to</legend>
          {info.tenants.map((t) => (
            <ChoiceCard
              key={t.id}
              name="scope"
              value={t.id}
              checked={choice === t.id}
              onChange={() => setChoice(t.id)}
              title={t.name}
              className="px-4 py-3 has-checked:border-line-focus has-checked:bg-selected"
              badge={
                <>
                  {t.status === "pending" ? (
                    <span className="text-caption font-normal text-fg-3">In review</span>
                  ) : null}
                  <span className="text-caption font-normal text-fg-3">{roleWord(t.role)}</span>
                </>
              }
            />
          ))}
          {info.operator ? (
            <ChoiceCard
              name="scope"
              value="operator"
              checked={choice === "operator"}
              onChange={() => setChoice("operator")}
              title="The operator view of every project"
              description="Daily totals, search and compare"
              className="px-4 py-3 has-checked:border-line-focus has-checked:bg-selected [&>span:last-child]:pl-5"
            />
          ) : null}
        </fieldset>
      )}
      <Scope operator={operator} />
      <FormMessage error={approve.error} />
      <FormMessage error={deny.error} />
      <div className="flex flex-col gap-3">
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            pending={deny.isPending || deny.isSuccess}
            className="h-10 flex-1 max-sm:h-(--vs-control-touch)"
            onClick={() => deny.mutate()}
          >
            Deny
          </Button>
          <Button
            type="submit"
            disabled={busy || !choice}
            pending={approve.isPending || approve.isSuccess}
            className="h-10 flex-1 max-sm:h-(--vs-control-touch)"
          >
            {approve.isPending || approve.isSuccess ? "Approving" : "Approve"}
          </Button>
        </div>
        <p className="flex flex-wrap items-baseline justify-center gap-x-2 text-small text-fg-2">
          <span>Then your browser goes back to</span>
          <code className="font-mono break-all text-fg">{info.redirect_origin}</code>
        </p>
      </div>
    </form>
  );
}

// the role as the api names it, written as a word
function roleWord(role: string): string {
  return role ? role.slice(0, 1).toUpperCase() + role.slice(1) : role;
}

// the two ends of the connection and what passes between them
function Ends({ client }: { client: string }) {
  const end = "flex w-30 flex-col items-center gap-2 text-small text-fg-2";
  const icon =
    "flex size-14 items-center justify-center rounded-2xl border border-line-2 bg-raised text-section font-semibold text-fg leading-5";
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start">
      <span className={end}>
        <span aria-hidden className={icon}>
          {client.trim().slice(0, 1).toUpperCase()}
        </span>
        <span className="max-w-full truncate text-center">{client}</span>
      </span>
      <span className="relative flex h-14 items-center justify-center">
        <span
          aria-hidden
          className="absolute inset-x-0 top-1/2 border-t-2 border-dashed border-line-strong"
        />
        <span className="relative inline-flex h-7 items-center gap-1.5 rounded-full border border-line-2 bg-ground px-3 text-small text-fg">
          <Lock aria-hidden className="size-3" />
          Read-only
        </span>
      </span>
      <span className={end}>
        <span aria-hidden className={icon}>
          <Mark className="size-6" />
        </span>
        Veristar
      </span>
    </div>
  );
}

// what the grant allows, what it does not and when it ends, side by side
function Scope({ operator }: { operator: boolean }) {
  const col = "flex flex-col gap-1.5 px-4 py-3";
  const head = "text-caption font-semibold text-fg-2";
  return (
    <div
      data-scope={operator ? "operator" : "tenant"}
      className="grid grid-cols-1 divide-line-subtle overflow-hidden rounded-xl border border-line text-small leading-5 max-sm:divide-y sm:grid-cols-3 sm:divide-x"
    >
      <div className={col}>
        <h2 className={head}>It can read</h2>
        <p className="flex items-start gap-2 text-fg">
          <Check aria-hidden className="mt-1 size-3.5 shrink-0 text-success" />
          {operator
            ? "Daily totals of every project, search and compare"
            : "Page views, events and visits; API request counts, errors and latency"}
        </p>
      </div>
      <div className={col}>
        <h2 className={head}>It cannot</h2>
        <p className="flex items-start gap-2 text-fg">
          <X aria-hidden className="mt-1 size-3.5 shrink-0 text-fg-3" />
          {operator ? "Change anything" : "Change anything, or read your other projects"}
        </p>
      </div>
      <div className={col}>
        <h2 className={head}>It ends when</h2>
        <p className="text-fg">
          {operator
            ? "You stop being an operator, or you revoke the key"
            : "You leave the project, it is suspended or removed, or you revoke it on its AI assistants page"}
        </p>
      </div>
    </div>
  );
}
