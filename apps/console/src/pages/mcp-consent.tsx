import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "cn";
import { CircleAlert, Plug, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { type ConsentRequest, mcpApi, mcpKeys } from "@/api/mcp";
import { meQuery } from "@/api/queries";
import { ErrorPanel, FormMessage, Loading } from "@/components/error-panel";
import { PublicFrame } from "@/components/public-frame";
import { Button } from "@/components/ui/button";

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

export function McpConsentPage() {
  const [params] = useState(() => new URLSearchParams(window.location.search));
  const error = params.get("error");
  const [req] = useState(() => (error ? null : takeRequest()));

  return (
    <PublicFrame center={false} className="items-center">
      <div className="w-full max-w-dialog-sm">
        {error ? (
          <Stop
            tone="critical"
            title="The connection was not set up"
            text={AUTHORIZE_ERRORS[error] ?? params.get("error_description") ?? error}
            alert
          />
        ) : req ? (
          <Signed request={req} />
        ) : (
          <Stop
            tone="info"
            title="No connection request"
            text="Start from your AI client: add the Veristar server URL as a connector and it opens this page."
          />
        )}
      </div>
    </PublicFrame>
  );
}

// a card that explains why there is nothing to approve, with the way back
function Stop({
  tone,
  title,
  text,
  children,
  alert = false,
}: {
  tone: "info" | "critical";
  title: string;
  text?: string;
  children?: React.ReactNode;
  alert?: boolean;
}) {
  return (
    <section className="flex flex-col gap-4 rounded-xl border border-line bg-raised p-6">
      <span
        className={cn(
          "flex size-10 items-center justify-center rounded-lg border border-line-strong [&_svg]:size-5",
          tone === "critical" ? "text-critical" : "text-fg-2",
        )}
      >
        {tone === "critical" ? <CircleAlert aria-hidden /> : <Plug aria-hidden />}
      </span>
      <div className="flex flex-col gap-2">
        <h1 className="text-section text-fg">{title}</h1>
        {text ? (
          <p role={alert ? "alert" : undefined} className="text-body text-fg-2">
            {text}
          </p>
        ) : null}
        {children}
      </div>
      <BackLink />
    </section>
  );
}

function BackLink() {
  return (
    <div>
      <Button asChild variant="outline" className="max-sm:h-(--vs-control-touch)">
        <Link to="/">Go to the console</Link>
      </Button>
    </div>
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
      <div className="rounded-xl border border-line bg-raised p-6">
        <Loading label="Loading the request" rows={3} />
      </div>
    );
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  if (inspect.error)
    return (
      <Stop tone="critical" title="This request cannot be approved">
        <FormMessage error={inspect.error} />
        <p className="text-small text-fg-2">
          Requests expire after 10 minutes. Start again from the AI client.
        </p>
      </Stop>
    );
  if (!inspect.data) return null;
  return <Consent request={request} info={inspect.data} login={me.data?.user.github_login ?? ""} />;
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

  return (
    <form
      aria-label="Approve the connection"
      className="flex flex-col rounded-xl border border-line bg-raised"
      onSubmit={(e) => {
        e.preventDefault();
        if (choice) approve.mutate();
      }}
    >
      <div className="flex flex-col gap-2.5 border-b border-line px-5 pt-6 pb-5 sm:px-6">
        <h1 className="text-section text-fg">Connect {info.client_name} to Veristar</h1>
        <p className="text-body text-fg-2">
          Signed in as <span className="font-medium text-fg">{login}</span>.{" "}
          <span className="font-medium text-fg">{info.client_name}</span> asks to read analytics as
          you. After approval your browser returns to{" "}
          <code className="font-mono text-small break-all text-fg">{info.redirect_origin}</code>.
        </p>
      </div>
      <ul className="flex flex-col gap-2.5 border-b border-line px-5 py-4 text-small sm:px-6">
        <li className="flex gap-2.5">
          <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
          <span>
            <span className="font-medium text-fg">Read-only.</span>{" "}
            <span className="text-fg-2">
              Web traffic, events and sessions as Umami holds them, and API request counts, errors
              and latency. Nothing can be changed.
            </span>
          </span>
        </li>
        <li className="flex gap-2.5">
          <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
          <span>
            <span className="font-medium text-fg">One project only, the one you choose.</span>{" "}
            <span className="text-fg-2">
              It ends when you leave the project, the project is suspended or removed, or you revoke
              it on the project's AI page.
            </span>
          </span>
        </li>
      </ul>
      <div className="px-5 py-4 sm:px-6">
        {nothing ? (
          <p
            role="alert"
            className="flex items-start gap-2 rounded-md border border-warning-line bg-warning-soft px-3 py-2.5 text-small text-fg"
          >
            <CircleAlert aria-hidden className="mt-0.5 size-3.5 shrink-0 text-warning" />
            You are not a member of a project that can be connected.
          </p>
        ) : (
          <fieldset className="flex flex-col gap-1.5">
            <legend className="mb-2 text-small font-medium text-fg">Give access to</legend>
            {info.tenants.map((t) => (
              <Choice
                key={t.id}
                checked={choice === t.id}
                onChange={() => setChoice(t.id)}
                name={t.name}
                detail={`${t.slug}, ${t.role}${t.status === "pending" ? ", in review" : ""}`}
              />
            ))}
            {info.operator ? (
              <Choice
                checked={choice === "operator"}
                onChange={() => setChoice("operator")}
                name="The operator view of every project"
                detail="daily aggregates, search and compare"
              />
            ) : null}
          </fieldset>
        )}
        {approve.error ? <FormMessage error={approve.error} className="mt-3" /> : null}
        {deny.error ? <FormMessage error={deny.error} className="mt-3" /> : null}
      </div>
      <div className="flex flex-wrap gap-2 border-t border-line px-5 py-4 sm:px-6">
        <Button
          type="submit"
          disabled={busy || !choice}
          pending={approve.isPending || approve.isSuccess}
          className="max-sm:h-(--vs-control-touch) max-sm:flex-1"
        >
          {approve.isPending || approve.isSuccess ? "Approving" : "Approve"}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          pending={deny.isPending || deny.isSuccess}
          className="max-sm:h-(--vs-control-touch) max-sm:flex-1"
          onClick={() => deny.mutate()}
        >
          Deny
        </Button>
      </div>
    </form>
  );
}

// one scope as a selectable card; the native radio keeps keyboard and
// screen reader behaviour, the card only draws the choice
function Choice({
  checked,
  onChange,
  name,
  detail,
}: {
  checked: boolean;
  onChange: () => void;
  name: string;
  detail: string;
}) {
  return (
    <label
      className={cn(
        "grid cursor-pointer grid-cols-[auto_minmax(0,1fr)] items-start gap-x-2.5 rounded-lg border px-3 py-2.5 transition-colors duration-(--vs-duration-press)",
        checked ? "border-accent bg-selected" : "border-line hover:bg-hover",
      )}
    >
      <input
        type="radio"
        name="scope"
        className="mt-0.5 size-4 accent-accent focus-ring"
        checked={checked}
        onChange={onChange}
      />
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-body font-medium text-fg">{name}</span>
        <span className="text-caption font-normal text-fg-2">{detail}</span>
      </span>
    </label>
  );
}
