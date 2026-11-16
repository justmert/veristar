import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { ArrowLeft, ArrowRight, Check, CircleAlert, ExternalLink } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useId, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { webDashboardUrl } from "@/api/endpoints";
import { ingestDayQuery, ingestMonthQuery, sitesQuery, snippetQuery } from "@/api/queries";
import type { Ingest, Site, Snippet } from "@/api/schemas";
import { CodeBlock, CopyField } from "@/components/code-block";
import { CopyButton } from "@/components/copy-button";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { NativeSelect } from "@/components/form-field";
import { PageHeader } from "@/components/panel";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { isNotFound } from "@/lib/errors";
import { formatBytes, formatCount, formatDate } from "@/lib/format";
import { outcomeHint, rejectionRows } from "@/lib/outcomes";
import { CONSENT_START, readsTitleChoice, snippetGuides } from "@/lib/snippet-guides";
import { NotFoundPage } from "./not-found";
import { useProject } from "./project-layout";
import { type FirstEvent, firstEvent, sitePath, snippetPath, verifyPath } from "./sites";

// ------------------------------------------------------------ setup frame

// rows are folded every few minutes, so a 30 s poll finds a first event soon
// after it is countable without asking more often than it can change
const FIRST_EVENT_POLL = 30_000;

// the setup of one site as one flow: the snippet and the verify urls are its
// two steps under a shared header with the progress of all three
export function SetupFrame({
  site,
  step,
  event,
  children,
}: {
  site: Site | undefined;
  step: "install" | "verify";
  event: FirstEvent | null;
  children: ReactNode;
}) {
  const { tenant } = useProject();
  const done = site
    ? 1 + (event === "received" ? 1 : 0) + (site.verification === "verified" ? 1 : 0)
    : 1;
  const next =
    event !== "received"
      ? "Next: first event."
      : site?.verification !== "verified"
        ? "Next: verify the domain."
        : "";
  const status =
    done === 3
      ? "Setup complete."
      : `${event === "received" ? "Tracker installed." : "Site added."} ${next}`;
  return (
    <div className="flex flex-col gap-5">
      <Link
        to={`/projects/${tenant.id}`}
        className="inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring"
      >
        <ArrowLeft aria-hidden className="size-3.5" />
        Overview
      </Link>
      <PageHeader
        title={`Set up ${site?.name ?? "the site"}`}
        description={
          <span className="flex flex-wrap items-center gap-3">
            <span
              role="progressbar"
              aria-label="Setup progress"
              aria-valuemin={0}
              aria-valuemax={3}
              aria-valuenow={done}
              className="flex gap-1"
            >
              {[0, 1, 2].map((i) => (
                <span
                  key={i}
                  className={cn(
                    "h-1.5 w-8 rounded-full transition-colors duration-(--vs-duration-change) ease-standard",
                    i < done ? "bg-success" : "bg-selected",
                  )}
                />
              ))}
            </span>
            <span className="text-small text-fg-2">{status}</span>
          </span>
        }
      />
      {site ? <SetupSteps site={site} step={step} event={event} /> : null}
      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_17rem]">
        <div className="flex min-w-0 animate-rise-in flex-col gap-6">{children}</div>
        {site ? <SetupAside site={site} /> : null}
      </div>
    </div>
  );
}

function SetupSteps({
  site,
  step,
  event,
}: {
  site: Site;
  step: "install" | "verify";
  event: FirstEvent | null;
}) {
  const steps = [
    {
      key: "install",
      label: "Install and first event",
      to: snippetPath(site),
      done: event === "received",
    },
    {
      key: "verify",
      label: "Verify the domain",
      to: verifyPath(site),
      done: site.verification === "verified",
    },
  ];
  return (
    <nav aria-label="Setup steps">
      <ol className="flex flex-wrap gap-2">
        {steps.map((s, i) => {
          const current = s.key === step;
          return (
            <li key={s.key}>
              <Link
                to={s.to}
                aria-current={current ? "step" : undefined}
                className={cn(
                  "flex h-9 items-center gap-2 rounded-md border px-3 text-small transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-(--vs-control-touch)",
                  current
                    ? "border-fg-disabled bg-selected text-fg"
                    : "border-line text-fg-2 hover:bg-hover hover:text-fg",
                )}
              >
                <span
                  className={cn(
                    "flex size-5 items-center justify-center rounded-full font-mono text-axis",
                    s.done ? "bg-success text-sunken" : "border border-line-strong text-fg",
                  )}
                >
                  {s.done ? <Check aria-hidden className="size-3" strokeWidth={3} /> : i + 1}
                </span>
                {s.label}
                {s.done ? <span className="sr-only">(done)</span> : null}
              </Link>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}

function SetupAside({ site }: { site: Site }) {
  return (
    <aside aria-label="This site" className="flex flex-col gap-4 lg:sticky lg:top-6">
      <div className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4">
        <span className="text-caption tracking-wide text-fg-3 uppercase">This site</span>
        <dl className="flex flex-col gap-3 text-small">
          <div className="flex flex-col gap-0.5">
            <dt className="text-caption font-normal text-fg-3">Domain</dt>
            <dd className="break-all font-mono text-fg">{site.domain}</dd>
          </div>
          <div className="flex flex-col gap-0.5">
            <dt className="text-caption font-normal text-fg-3">Allowed origins</dt>
            <dd className="flex flex-col gap-0.5">
              {site.origins.map((o) => (
                <span key={o} className="break-all font-mono text-fg">
                  {o}
                </span>
              ))}
              <Link
                to={sitePath(site, "settings")}
                className="mt-1 w-fit rounded-sm text-fg-2 underline decoration-line-strong underline-offset-4 transition-colors hover:text-fg hover:decoration-fg focus-ring"
              >
                Edit origins
              </Link>
            </dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-caption font-normal text-fg-3">Site id</dt>
            <dd>
              <CopyField value={site.id} label="site id" />
            </dd>
          </div>
        </dl>
      </div>
      <div className="flex flex-col gap-2 rounded-lg border border-line p-4 text-small">
        <span className="text-caption tracking-wide text-fg-3 uppercase">Why verify</span>
        <p className="text-fg-2">
          Operators of the service see which domains a project has proven. It is not needed to
          collect data.
        </p>
      </div>
      <Link
        to={sitePath(site, "settings")}
        className="w-fit rounded-sm text-small text-fg-2 transition-colors hover:text-fg focus-ring"
      >
        Skip setup, go to site settings
      </Link>
    </aside>
  );
}

// the first event state from the ingest counts the setup already reads; the
// last 24 hours are polled while nothing has arrived
export function useFirstEvent(siteId: string): {
  event: FirstEvent | null;
  day: Ingest | undefined;
  month: Ingest | undefined;
} {
  const month = useQuery(ingestMonthQuery(siteId));
  const monthReceived = month.data !== undefined && firstEvent(month.data) === "received";
  const day = useQuery({
    ...ingestDayQuery(siteId),
    refetchInterval: (q) =>
      monthReceived || (q.state.data && firstEvent(q.state.data) === "received")
        ? false
        : FIRST_EVENT_POLL,
  });
  const known = month.data !== undefined || day.data !== undefined;
  const event = known ? firstEvent(month.data, day.data) : null;
  return { event, day: day.data, month: month.data };
}

// ------------------------------------------------------------ install

export function SnippetPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const version = params.get("version");
  const sites = useQuery(sitesQuery(tenant.id));
  const snippet = useQuery(snippetQuery(siteId, version));
  const first = useFirstEvent(siteId);

  const site = sites.data?.find((s) => s.id === siteId);
  if (isNotFound(snippet.error)) return <NotFoundPage />;

  return (
    <SetupFrame site={site} step="install" event={first.event}>
      {snippet.isPending ? <Loading label="Loading the snippet" /> : null}
      {snippet.error ? (
        <ErrorPanel error={snippet.error} onRetry={() => snippet.refetch()} />
      ) : null}
      {snippet.data ? (
        <Install
          snippet={snippet.data}
          site={site}
          first={first}
          onVersion={(v) => {
            const next = new URLSearchParams(params);
            next.set("version", v);
            setParams(next, { replace: true });
          }}
        />
      ) : null}
    </SetupFrame>
  );
}

function StepNumber({ n, done }: { n: number; done?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-7 shrink-0 items-center justify-center rounded-full font-mono text-small transition-colors duration-(--vs-duration-change) ease-standard",
        done ? "bg-success text-sunken" : "border border-line-strong bg-raised text-fg",
      )}
    >
      {done ? <Check className="size-4" strokeWidth={3} /> : n}
    </span>
  );
}

function Step({
  n,
  done,
  title,
  children,
  last,
}: {
  n: number;
  done?: boolean;
  title: ReactNode;
  children: ReactNode;
  last?: boolean;
}) {
  return (
    <li className="grid grid-cols-[1.75rem_minmax(0,1fr)] gap-x-4">
      <div className="flex flex-col items-center">
        <StepNumber n={n} done={done} />
        {last ? null : <span aria-hidden className="mt-2 w-px flex-1 bg-line" />}
      </div>
      <div className={cn("flex min-w-0 flex-col gap-3", last ? "" : "pb-8")}>
        <h3 className="pt-0.5 text-title text-fg">{title}</h3>
        {children}
      </div>
    </li>
  );
}

// a radio group of framework choices that wraps on a phone; arrow keys move
// the choice, as in a native radio group
function FrameworkPicker({
  value,
  options,
  onChange,
}: {
  value: string;
  options: { id: string; label: string }[];
  onChange: (id: string) => void;
}) {
  function key(e: React.KeyboardEvent<HTMLButtonElement>, i: number) {
    const d =
      e.key === "ArrowRight" || e.key === "ArrowDown"
        ? 1
        : e.key === "ArrowLeft" || e.key === "ArrowUp"
          ? -1
          : 0;
    if (!d) return;
    e.preventDefault();
    const j = (i + d + options.length) % options.length;
    const next = options[j];
    if (!next) return;
    onChange(next.id);
    (e.currentTarget.parentElement?.children[j] as HTMLElement | undefined)?.focus();
  }
  return (
    <div role="radiogroup" aria-label="Framework" className="flex flex-wrap gap-2">
      {options.map((o, i) => {
        const on = o.id === value;
        return (
          // biome-ignore lint/a11y/useSemanticElements: a choice chip that wraps, keyed like a native radio group
          <button
            key={o.id}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            onClick={() => onChange(o.id)}
            onKeyDown={(e) => key(e, i)}
            className={cn(
              "h-8 rounded-md border px-3 text-small transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-(--vs-control-touch)",
              on
                ? "border-fg-disabled bg-selected text-fg"
                : "border-line-strong text-fg-2 hover:bg-hover hover:text-fg",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function Install({
  snippet,
  site,
  first,
  onVersion,
}: {
  snippet: Snippet;
  site: Site | undefined;
  first: ReturnType<typeof useFirstEvent>;
  onVersion: (v: string) => void;
}) {
  const [fw, setFw] = useState("html");
  const [consent, setConsent] = useState(false);
  const [title, setTitle] = useState(false);
  const pickerId = useId();
  const consentId = useId();
  const titleId = useId();
  const titleChoice = readsTitleChoice(snippet.version);
  const guides = snippetGuides(snippet, { consent, title });
  const g = guides.find((x) => x.id === fw) ?? guides[0];
  const v = snippet.versions.find((x) => x.version === snippet.version);
  if (!g) return null;
  const npm = g.id === "npm";

  return (
    <section aria-label="Install the tracker" className="flex flex-col gap-4">
      <h2 className="text-section text-fg">Install the tracker</h2>
      <ol className="flex flex-col">
        <Step n={1} title="Pick how the site is built">
          <FrameworkPicker value={g.id} options={guides} onChange={setFw} />
        </Step>

        <Step n={2} title={npm ? "Install the package" : "Paste the tag"}>
          <p className="text-small text-fg-2">
            <span className="text-fg">{g.where}</span>
            {v ? (
              <span className="text-fg-3">
                {" "}
                · tracker {v.version}, {formatBytes(v.gzip_bytes)} compressed, pinned
              </span>
            ) : null}
          </p>
          <div className="overflow-hidden rounded-lg border border-line bg-sunken">
            <div className="flex min-h-10 items-center justify-between gap-3 border-b border-line px-3 py-1">
              <span className="truncate text-caption text-fg-3">{g.label}</span>
              <div className="flex items-center gap-2">
                <CopyButton
                  text={g.copy}
                  label={npm ? "Copy command" : "Copy tag"}
                  size="xs"
                  variant="ghost"
                />
              </div>
            </div>
            <pre className="overflow-x-auto px-3.5 py-3 font-mono text-small break-all whitespace-pre-wrap text-fg">
              <code>{g.code}</code>
            </pre>
          </div>
          <p className="max-w-prose text-small text-fg-2">{g.note}</p>
          <p className="max-w-prose text-small text-fg-2">
            The integrity hash makes the browser refuse any other file, so the code on your pages
            changes only when you change the tag on purpose.
          </p>

          {npm ? null : (
            <div className="flex flex-col gap-2 rounded-lg border border-line p-3">
              <div className="flex items-start gap-3">
                <Checkbox
                  id={consentId}
                  checked={consent}
                  onCheckedChange={(c) => setConsent(c === true)}
                  aria-describedby={`${consentId}-d`}
                  className="mt-0.5"
                />
                <label htmlFor={consentId} className="text-body text-fg">
                  Wait for consent before measuring
                </label>
              </div>
              <p id={`${consentId}-d`} className="pl-7 text-small text-fg-2">
                Adds <code className="font-mono text-fg">data-auto-track="false"</code> to the tag.
                The tracker then loads but sends nothing and reads no storage until your consent
                banner calls <code className="font-mono text-fg">umami.start()</code>. Your site
                keeps the visitor's choice and calls it again on later pages.
              </p>
              <AnimatePresence initial={false}>
                {consent ? (
                  <motion.div {...fade} className="pl-7">
                    <CodeBlock
                      code={CONSENT_START}
                      label="When the visitor agrees"
                      copyLabel="Copy call"
                    />
                  </motion.div>
                ) : null}
              </AnimatePresence>
            </div>
          )}

          {npm || !titleChoice ? null : (
            <div className="flex flex-col gap-2 rounded-lg border border-line p-3">
              <div className="flex items-start gap-3">
                <Checkbox
                  id={titleId}
                  checked={title}
                  onCheckedChange={(c) => setTitle(c === true)}
                  aria-describedby={`${titleId}-d`}
                  className="mt-0.5"
                />
                <label htmlFor={titleId} className="text-body text-fg">
                  Send the page title
                </label>
              </div>
              <p id={`${titleId}-d`} className="pl-7 text-small text-fg-2">
                Adds <code className="font-mono text-fg">data-send-title="true"</code> to the tag.
                Without it the tracker sends the page's path and referrer only. No metric needs the
                title, and a title can name a person, so turn this on only if your titles are
                generic.
              </p>
            </div>
          )}

          <details className="group rounded-lg border border-line text-small">
            <summary className="flex min-h-10 cursor-pointer items-center gap-2 rounded-lg px-3 text-fg-2 transition-colors hover:text-fg focus-ring">
              <ArrowRight
                aria-hidden
                className="size-3.5 transition-transform duration-(--vs-duration-press) group-open:rotate-90"
              />
              Version and what the tag pins
            </summary>
            <div className="flex flex-col gap-3 border-t border-line p-3">
              <div className="flex flex-wrap items-end gap-3">
                <div className="flex flex-col gap-1.5">
                  <label htmlFor={pickerId} className="text-small font-medium text-fg">
                    Tracker version
                  </label>
                  <NativeSelect
                    id={pickerId}
                    value={snippet.version}
                    onChange={(e) => onVersion(e.target.value)}
                  >
                    {snippet.versions.map((ver, i) => (
                      <option key={ver.version} value={ver.version}>
                        {ver.version}
                        {i === 0 ? " (recommended)" : ""}
                      </option>
                    ))}
                  </NativeSelect>
                </div>
                {v ? (
                  <p className="pb-1.5 text-small text-fg-2">
                    Released {formatDate(`${v.released}T00:00:00Z`)}, {formatBytes(v.gzip_bytes)}{" "}
                    compressed ({formatBytes(v.bytes)} raw)
                  </p>
                ) : null}
              </div>
              <dl className="grid gap-x-4 gap-y-2 sm:grid-cols-[6rem_minmax(0,1fr)] sm:items-center">
                <dt className="text-fg-2">Script</dt>
                <dd className="min-w-0">
                  <CopyField value={snippet.src} label="script address" />
                </dd>
                <dt className="text-fg-2">Integrity</dt>
                <dd className="min-w-0">
                  <CopyField value={snippet.integrity} label="integrity" />
                </dd>
                <dt className="text-fg-2">Site id</dt>
                <dd className="min-w-0">
                  <CopyField value={snippet.site_id} label="site id" />
                </dd>
              </dl>
              <p className="text-fg-2">
                The browser refuses the script if a single byte differs from this hash. To move to a
                newer tracker, pick it above and replace the tag on your pages.
              </p>
            </div>
          </details>
        </Step>

        <Step n={3} title="First event" done={first.event === "received"} last>
          <FirstEventState site={site} first={first} />
        </Step>
      </ol>
    </section>
  );
}

function FirstEventState({
  site,
  first,
}: {
  site: Site | undefined;
  first: ReturnType<typeof useFirstEvent>;
}) {
  const state = first.event;
  // the month answer is cached longer than the polled day, so a refusal seen
  // in the last 24 hours is named even before the month catches up
  const monthReasons = rejectionRows(first.month?.totals ?? {});
  const reasons = (
    monthReasons.length > 0 ? monthReasons : rejectionRows(first.day?.totals ?? {})
  ).slice(0, 3);
  const period = monthReasons.length > 0 ? "30 days" : "24 hours";
  const originRefused = reasons.some((r) => r.key === "rejected_origin");
  const counted = (first.month?.totals.accepted ?? 0) + (first.month?.totals.quarantined ?? 0);
  return (
    <div aria-live="polite" className="min-h-24">
      <AnimatePresence mode="wait" initial={false}>
        {state === null ? (
          <motion.p key="checking" {...fade} className="text-small text-fg-2">
            Checking for events.
          </motion.p>
        ) : state === "received" ? (
          <motion.div
            key="received"
            {...fade}
            className="flex animate-highlight flex-col gap-3 rounded-lg border border-success-line bg-success-soft p-4"
          >
            <span className="flex items-center gap-2 text-body font-medium text-fg">
              <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
              First event received
            </span>
            <span className="text-small text-fg-2">
              {counted > 0
                ? `${formatCount(counted)} events from this site were counted in the last 30 days. `
                : "Events from this site were counted. "}
              The web dashboard is ready, and counts update every few minutes.
            </span>
            {site ? (
              <div className="flex flex-wrap gap-2">
                <Button asChild size="sm" className="max-lg:h-(--vs-control-touch)">
                  <Link to={verifyPath(site)}>
                    Continue to verification
                    <ArrowRight aria-hidden />
                  </Link>
                </Button>
                <Button
                  asChild
                  variant="outline"
                  size="sm"
                  className="max-lg:h-(--vs-control-touch)"
                >
                  <a href={webDashboardUrl(site.id, "primary")}>
                    Open web dashboard
                    <ExternalLink aria-hidden />
                  </a>
                </Button>
              </div>
            ) : null}
          </motion.div>
        ) : state === "refused" ? (
          <motion.div
            key="refused"
            {...fade}
            className="flex flex-col gap-3 rounded-lg border border-warning-line bg-warning-soft p-4"
          >
            <span className="flex items-center gap-2 text-body font-medium text-fg">
              <CircleAlert aria-hidden className="size-4 text-warning" />
              Events are arriving, but they are refused
            </span>
            <ul className="flex flex-col gap-1.5 text-small">
              {reasons.map((r) => (
                <li key={r.key} className="flex flex-col">
                  <span className="text-fg">
                    {r.label}: <span className="font-mono">{formatCount(r.count)}</span> in the last{" "}
                    {period}
                  </span>
                  {outcomeHint(r.key) ? (
                    <span className="text-fg-2">{outcomeHint(r.key)}</span>
                  ) : null}
                </li>
              ))}
            </ul>
            {originRefused ? (
              <span className="text-small text-fg-2">
                A common cause is a www. or staging host that is not in the allowed origins.
              </span>
            ) : null}
            {site ? (
              <div className="flex flex-wrap gap-2">
                {originRefused ? (
                  <Button asChild size="sm" className="max-lg:h-(--vs-control-touch)">
                    <Link to={sitePath(site, "settings")}>Edit allowed origins</Link>
                  </Button>
                ) : null}
                <Button
                  asChild
                  variant="outline"
                  size="sm"
                  className="max-lg:h-(--vs-control-touch)"
                >
                  <Link to={sitePath(site)}>See every outcome</Link>
                </Button>
              </div>
            ) : null}
          </motion.div>
        ) : (
          <motion.div key="waiting" {...fade} className="flex flex-col gap-2">
            <span className="flex items-center gap-2.5 text-body font-medium text-fg">
              <StatusDot tone="accent" live className="size-2" />
              Listening for the first event
            </span>
            <span className="max-w-prose text-small text-fg-2">
              Deploy the tag, then open{" "}
              <span className="font-mono text-fg">{site?.domain ?? "the site"}</span> in a browser.
              This step completes on its own once the first events are counted, usually within a few
              minutes.
            </span>
            {site ? (
              <a
                href={`https://${site.domain}`}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg underline decoration-line-strong underline-offset-4 transition-colors hover:decoration-fg focus-ring"
              >
                Open {site.domain}
                <ExternalLink aria-hidden className="size-3.5" />
              </a>
            ) : null}
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
