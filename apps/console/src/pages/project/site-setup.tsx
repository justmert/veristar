import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { Check, ChevronRight, CircleAlert, ExternalLink, Settings } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useEffect, useId, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { sitesQuery, snippetQuery } from "@/api/queries";
import type { Ingest, Site, Snippet } from "@/api/schemas";
import { CodeBlock, CopyField } from "@/components/code-block";
import { StellarTomlSummary } from "@/components/evidence";
import { NativeSelect } from "@/components/form-field";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { ErrorState } from "@/components/states";
import { StatusDot } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { isNotFound } from "@/lib/errors";
import { formatBytes, formatCount, formatDate } from "@/lib/format";
import { outcomeHint, rejectionRows } from "@/lib/outcomes";
import {
  type FirstEvent,
  nextText,
  type SetupStep,
  type StepKey,
  setupProgress,
  setupStatus,
  setupSteps,
  siteSettingsPath,
} from "@/lib/site-setup";
import { CONSENT_START, readsTitleChoice, snippetGuides } from "@/lib/snippet-guides";
import { webPath } from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { NotFoundPage } from "@/pages/public/not-found";
import { SitePageLoading, SitesBackLink } from "./page-frame";
import { VerifyDomain } from "./site-verify";
import { useFirstEvent } from "./sites";

// the setup of one site as one guided task: install the tag, see the first
// event arrive, verify the domain, and the stellar.toml the service reads.
// one progress model (lib/site-setup.ts) drives the header, the steps and
// the sites list.
export function SiteSetupPage() {
  const { tenant } = useProject();
  const { siteId = "" } = useParams();
  const sites = useQuery(sitesQuery(tenant.id));
  const site = sites.data?.find((s) => s.id === siteId && s.status !== "removed");

  if (sites.data && !site) return <NotFoundPage />;
  if (sites.error)
    return (
      <div className="flex flex-col gap-6">
        <SitesBackLink tenantId={tenant.id} />
        <PageHeader title="Site setup" />
        <ErrorState error={sites.error} onRetry={() => sites.refetch()} />
      </div>
    );
  if (!site) return <SitePageLoading tenantId={tenant.id} page="setup" />;
  return <SiteSetup site={site} />;
}

function SiteSetup({ site }: { site: Site }) {
  const [params, setParams] = useSearchParams();
  const version = params.get("version");
  const snippet = useQuery(snippetQuery(site.id, version));
  const first = useFirstEvent(site);
  const steps = setupSteps(site, first.event);
  const status = setupStatus(site, first.event);
  const { done, total } = setupProgress(steps);
  const step = (k: StepKey) => steps.find((s) => s.key === k) as SetupStep;
  // a link to one step (#domain) scrolls to it once the page is drawn
  const { hash } = useLocation();
  useEffect(() => {
    if (!hash) return;
    // after the shell's own scroll to the top of a new page
    const frame = requestAnimationFrame(() =>
      document.getElementById(hash.slice(1))?.scrollIntoView?.({ block: "start" }),
    );
    return () => cancelAnimationFrame(frame);
  }, [hash]);

  if (isNotFound(snippet.error)) return <NotFoundPage />;

  return (
    <div className="flex flex-col gap-6">
      <SitesBackLink tenantId={site.tenant_id} />
      <PageHeader
        title={site.name}
        meta={site.status !== "active" ? <StatusBadge kind="site" status={site.status} /> : null}
        description={
          <span className="block truncate font-mono text-small" title={site.domain}>
            {site.domain}
          </span>
        }
        secondary={
          <Button asChild variant="outline">
            <Link to={siteSettingsPath(site)}>
              <Settings aria-hidden />
              Settings
            </Link>
          </Button>
        }
      />
      <Progress steps={steps} done={done} total={total} text={nextText(status)} />
      {site.in_review ? (
        <Notice tone="info" title="In review">
          This domain belonged to a removed project. An operator checks it before the site receives
          traffic.
        </Notice>
      ) : null}
      {site.status_reason ? (
        <Notice tone="info" title="Reason from an operator">
          {site.status_reason}
        </Notice>
      ) : null}

      <div className="grid items-start gap-8 lg:grid-cols-[minmax(0,1fr)_17rem]">
        <ol aria-label="Setup steps" className="flex min-w-0 flex-col">
          <Step n={1} step={step("install")} id="install" settled={first.event !== null}>
            {snippet.isPending ? (
              <div role="status" aria-busy="true" className="flex flex-col gap-3">
                <span className="sr-only">Loading the tag</span>
                <Skeleton className="h-8 w-2/3" />
                <Skeleton className="h-32 w-full" />
              </div>
            ) : snippet.error ? (
              <ErrorState error={snippet.error} onRetry={() => snippet.refetch()} compact />
            ) : (
              <Install
                snippet={snippet.data}
                onVersion={(v) => {
                  const next = new URLSearchParams(params);
                  next.set("version", v);
                  setParams(next, { replace: true });
                }}
              />
            )}
          </Step>
          <Step n={2} step={step("event")} id="event" keepOpen>
            <FirstEventState site={site} first={first} />
          </Step>
          <Step
            n={3}
            step={step("domain")}
            id="domain"
            description="Prove that the project controls the domain. Operators see the proof. Events arrive without it."
          >
            <VerifyDomain site={site} current={step("domain").state === "current"} />
          </Step>
          <Step
            n={4}
            step={step("toml")}
            id="toml"
            last
            description="The service reads it after setup and once a day. SCF reviewers see what it lists."
          >
            <StellarTomlSummary
              toml={site.stellar_toml}
              checkedAt={site.stellar_toml_checked_at}
              domain={site.domain}
            />
          </Step>
        </ol>
        <SiteAside site={site} />
      </div>
    </div>
  );
}

// the one progress line of the setup: a bar of the required steps and the
// next thing to do
function Progress({
  steps,
  done,
  total,
  text,
}: {
  steps: SetupStep[];
  done: number;
  total: number;
  text: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <span
        role="progressbar"
        aria-label="Setup progress"
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={done}
        aria-valuetext={`${done} of ${total} steps done`}
        className="flex gap-1"
      >
        {steps
          .filter((s) => !s.optional)
          .map((s) => (
            <span
              key={s.key}
              className={cn(
                "h-1.5 w-10 rounded-full transition-colors duration-(--vs-duration-change) ease-standard",
                s.state === "done"
                  ? "bg-success"
                  : s.state === "attention"
                    ? "bg-warning"
                    : "bg-selected",
              )}
            />
          ))}
      </span>
      <span className="text-small text-fg-2">
        <span className="tabular">
          {done} of {total}
        </span>{" "}
        done. {text}
      </span>
    </div>
  );
}

const STATE_WORD: Record<SetupStep["state"], string> = {
  done: "done",
  current: "to do now",
  todo: "not done yet",
  attention: "needs action",
};

function StepMark({ n, state }: { n: number; state: SetupStep["state"] }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-7 shrink-0 items-center justify-center rounded-full font-mono text-small transition-colors duration-(--vs-duration-change) ease-standard",
        state === "done"
          ? "bg-success text-ground"
          : state === "attention"
            ? "border border-warning-line text-warning"
            : state === "current"
              ? "border-2 border-accent-text text-accent-text"
              : "border border-line-strong text-fg-2",
      )}
    >
      {state === "done" ? (
        <Check className="size-4" strokeWidth={3} />
      ) : state === "attention" ? (
        <CircleAlert className="size-4" />
      ) : (
        n
      )}
    </span>
  );
}

// one step of the vertical list. a step that is done when the page opens
// folds its body away behind a "Show" toggle, so the page shows what is
// left to do; `keepOpen` keeps the body in view when it carries the step's
// result. a step that becomes done while the person works in it stays open:
// only the page load or the toggle folds it. `settled` says the step's state
// is known (the first event is still being read on load).
export function Step({
  n,
  step,
  id,
  description,
  last,
  keepOpen,
  settled = true,
  children,
}: {
  n: number;
  step: SetupStep;
  id: string;
  description?: string;
  last?: boolean;
  keepOpen?: boolean;
  settled?: boolean;
  children: ReactNode;
}) {
  const done = step.state === "done" && !keepOpen;
  // null until the state is known; then whether the body is folded
  const [folded, setFolded] = useState<boolean | null>(settled ? done : null);
  if (folded === null && settled) setFolded(done);
  // a step that is no longer done always shows what is left to do
  const open = !(done && folded === true);
  const titleId = useId();
  const bodyId = useId();
  return (
    <li
      id={id}
      className="grid scroll-mt-6 grid-cols-[1.75rem_minmax(0,1fr)] gap-x-4"
      data-step={step.key}
      data-state={step.state}
    >
      <div className="flex flex-col items-center">
        <StepMark n={n} state={step.state} />
        {last ? null : <span aria-hidden className="mt-2 w-px flex-1 bg-line" />}
      </div>
      <section
        aria-labelledby={titleId}
        className={cn("flex min-w-0 flex-col gap-3", last ? "" : "pb-8")}
      >
        <div className="flex min-h-7 flex-wrap items-center gap-x-3 gap-y-1">
          <h2 id={titleId} className="text-title text-fg">
            {step.title}
            <span className="sr-only">, {STATE_WORD[step.state]}</span>
          </h2>
          {step.optional ? <span className="text-caption text-fg-2">Optional</span> : null}
          {done ? (
            <Button
              variant="ghost"
              size="xs"
              aria-expanded={open}
              aria-controls={bodyId}
              onClick={() => setFolded(open)}
              className="max-lg:h-(--vs-control-touch)"
            >
              <ChevronRight
                aria-hidden
                className={cn(
                  "transition-transform duration-(--vs-duration-press) ease-standard",
                  open && "rotate-90",
                )}
              />
              {open ? "Hide" : "Show"}
            </Button>
          ) : null}
        </div>
        {description && open ? (
          <p className="max-w-prose text-small text-fg-2">{description}</p>
        ) : null}
        <AnimatePresence initial={false}>
          {open ? (
            <motion.div key="body" id={bodyId} {...fade} className="flex min-w-0 flex-col gap-3">
              {children}
            </motion.div>
          ) : null}
        </AnimatePresence>
      </section>
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
    <div role="radiogroup" aria-label="How the site is built" className="flex flex-wrap gap-2">
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
                ? "border-pressed-line bg-pressed text-fg"
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

// an option of the tag: a checkbox, what it changes, and an optional block
function TagOption({
  label,
  checked,
  onChange,
  children,
}: {
  label: string;
  checked: boolean;
  onChange: (on: boolean) => void;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
      <Checkbox
        id={id}
        checked={checked}
        onCheckedChange={(c) => onChange(c === true)}
        aria-describedby={`${id}-d`}
        className="mt-1"
      />
      <label htmlFor={id} className="text-body text-fg">
        {label}
      </label>
      <div id={`${id}-d`} className="col-start-2 flex min-w-0 flex-col gap-2 text-small text-fg-2">
        {children}
      </div>
    </div>
  );
}

function Install({ snippet, onVersion }: { snippet: Snippet; onVersion: (v: string) => void }) {
  const [fw, setFw] = useState("html");
  const [consent, setConsent] = useState(false);
  const [title, setTitle] = useState(false);
  const pickerId = useId();
  const titleChoice = readsTitleChoice(snippet.version);
  const guides = snippetGuides(snippet, { consent, title });
  const g = guides.find((x) => x.id === fw) ?? guides[0];
  const v = snippet.versions.find((x) => x.version === snippet.version);
  if (!g) return null;
  const npm = g.id === "npm";
  const code = (t: string) => <code className="font-mono text-fg">{t}</code>;

  return (
    <section aria-label="Install the tag" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-2">
        <span className="text-small font-medium text-fg">How is the site built?</span>
        <FrameworkPicker value={g.id} options={guides} onChange={setFw} />
      </div>
      <p className="text-small text-fg">
        {g.where}
        {v ? (
          <span className="text-fg-2">
            {" "}
            Tracker {v.version}, {formatBytes(v.gzip_bytes)} compressed.
          </span>
        ) : null}
      </p>
      <CodeBlock
        code={g.code}
        copyText={g.copy}
        label={g.label}
        copyLabel={npm ? "Copy command" : "Copy tag"}
        className="min-w-0"
      />
      {g.note ? <p className="max-w-prose text-small text-fg-2">{g.note}</p> : null}

      <div className="flex flex-col gap-3">
        <TagOption
          label="Wait for consent before measuring"
          checked={consent}
          onChange={setConsent}
        >
          <p>
            {npm ? (
              <>
                Passes {code("autoTrack: false")} to {code("init()")}.
              </>
            ) : (
              <>Adds {code('data-auto-track="false"')} to the tag.</>
            )}{" "}
            The tracker then sends nothing until your consent banner calls {code("umami.start()")}.
          </p>
          <AnimatePresence initial={false}>
            {consent ? (
              <motion.div {...fade}>
                <CodeBlock
                  code={CONSENT_START}
                  label="When the visitor agrees"
                  copyLabel="Copy call"
                />
              </motion.div>
            ) : null}
          </AnimatePresence>
        </TagOption>
        {titleChoice ? (
          <TagOption label="Send the page title" checked={title} onChange={setTitle}>
            <p>
              {npm ? (
                <>
                  Passes {code("sendTitle: true")} to {code("init()")}.
                </>
              ) : (
                <>Adds {code('data-send-title="true"')} to the tag.</>
              )}{" "}
              A title can name a person. Turn this on only if your titles are generic.
            </p>
          </TagOption>
        ) : null}
      </div>

      <details className="group rounded-lg border border-line text-small">
        <summary className="flex min-h-10 cursor-pointer list-none items-center gap-2 rounded-lg px-3 text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring max-lg:min-h-(--vs-control-touch) [&::-webkit-details-marker]:hidden">
          <ChevronRight
            aria-hidden
            className="size-4 transition-transform duration-(--vs-duration-press) ease-standard group-open:rotate-90"
          />
          Version and what the tag pins
        </summary>
        <div className="flex flex-col gap-3 border-t border-line p-3">
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex flex-col gap-1">
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
              <p className="pb-1 text-small text-fg-2">
                Released {formatDate(v.released)}. {formatBytes(v.gzip_bytes)} compressed,{" "}
                {formatBytes(v.bytes)} raw.
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
            The browser refuses the script if one byte differs from the integrity hash. To use a
            newer tracker, choose it above and replace the tag on your pages.
          </p>
        </div>
      </details>
    </section>
  );
}

function FirstEventState({
  site,
  first,
}: {
  site: Site;
  first: { event: FirstEvent | null; day: Ingest | undefined; month: Ingest | undefined };
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
    <div aria-live="polite" className="min-h-20">
      <AnimatePresence mode="wait" initial={false}>
        {state === null ? (
          <motion.p key="checking" {...fade} className="text-small text-fg-2">
            Checking for events.
          </motion.p>
        ) : state === "received" ? (
          <motion.div
            key="received"
            {...fade}
            data-arrived=""
            className="flex animate-highlight flex-col gap-3 rounded-lg border border-success-line bg-raised p-4"
          >
            <span className="flex items-center gap-2 text-body font-medium text-fg">
              <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
              First event arrived
            </span>
            <span className="text-small text-fg-2">
              {counted > 0
                ? `${formatCount(counted)} events from this site were counted in the last 30 days.`
                : "Page views from this site arrived today. Counts on the other pages follow within a few minutes."}
            </span>
            <div className="flex flex-wrap gap-2">
              <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
                <Link to={webPath(site.tenant_id, site.id)}>Open web data</Link>
              </Button>
            </div>
          </motion.div>
        ) : state === "refused" ? (
          <motion.div
            key="refused"
            {...fade}
            className="flex flex-col gap-3 rounded-lg border border-warning-line bg-raised p-4"
          >
            <span className="flex items-center gap-2 text-body font-medium text-fg">
              <CircleAlert aria-hidden className="size-4 text-warning" />
              Events arrive, but the service refuses them
            </span>
            <ul className="flex flex-col gap-2 text-small">
              {reasons.map((r) => (
                <li key={r.key} className="flex flex-col">
                  <span className="text-fg">
                    {r.label}: <span className="tabular">{formatCount(r.count)}</span> in the last{" "}
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
                Often a www. or staging host is missing from the allowed origins. Add it in the site
                settings.
              </span>
            ) : null}
            <div className="flex flex-wrap gap-2">
              {originRefused ? (
                <Button
                  asChild
                  variant="outline"
                  size="sm"
                  className="max-lg:h-(--vs-control-touch)"
                >
                  <Link to={siteSettingsPath(site)}>Edit allowed origins</Link>
                </Button>
              ) : null}
              <Button asChild variant="ghost" size="sm" className="max-lg:h-(--vs-control-touch)">
                <Link to={`${webPath(site.tenant_id, site.id)}#outcomes`}>See every outcome</Link>
              </Button>
            </div>
          </motion.div>
        ) : (
          <motion.div key="waiting" {...fade} className="flex flex-col gap-2">
            <span className="flex items-center gap-2 text-body font-medium text-fg">
              <StatusDot tone="info" live className="size-2" />
              Waiting for the first event
            </span>
            <span className="max-w-prose text-small text-fg-2">
              Install the tag, then open the site in a browser. This step completes by itself,
              usually within a few minutes.
            </span>
            <a
              href={`https://${site.domain}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex w-fit max-w-full items-center gap-1 rounded-sm text-small text-fg underline decoration-line-strong underline-offset-4 transition-colors duration-(--vs-duration-press) ease-standard hover:decoration-fg focus-ring"
            >
              <span className="truncate">Open {site.domain}</span>
              <ExternalLink aria-hidden className="size-4 shrink-0" />
            </a>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

function SiteAside({ site }: { site: Site }) {
  return (
    <aside
      aria-label="This site"
      className="flex min-w-0 flex-col gap-4 rounded-lg border border-line bg-raised p-4 lg:sticky lg:top-6"
    >
      <span className="text-caption text-fg-2">This site</span>
      <dl className="flex min-w-0 flex-col gap-3 text-small">
        <div className="flex min-w-0 flex-col gap-1">
          <dt className="text-caption text-fg-2">Domain</dt>
          <dd className="break-all font-mono text-fg" title={site.domain}>
            {site.domain}
          </dd>
        </div>
        <div className="flex min-w-0 flex-col gap-1">
          <dt className="text-caption text-fg-2">Allowed origins</dt>
          <dd className="flex min-w-0 flex-col gap-1">
            {site.origins.map((o) => (
              <span key={o} className="break-all font-mono text-fg" title={o}>
                {o}
              </span>
            ))}
            <Link
              to={siteSettingsPath(site)}
              className="mt-1 w-fit rounded-sm text-fg-2 underline decoration-line-strong underline-offset-4 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg hover:decoration-fg focus-ring"
            >
              Edit origins
            </Link>
          </dd>
        </div>
        <div className="flex min-w-0 flex-col gap-1">
          <dt className="text-caption text-fg-2">Site id</dt>
          <dd>
            <CopyField value={site.id} label="site id" />
          </dd>
        </div>
      </dl>
    </aside>
  );
}
