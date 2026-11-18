// the setup of one site as one model: the steps, their states, the status
// word of the site and its next action. the sites list, the site's setup
// page and its header all read it, so they never disagree.

import type { Ingest, Site } from "@/api/schemas";
import type { WebHourly } from "@/api/web";
import type { SetupStatus } from "@/components/status";
import { sumRejected } from "@/lib/outcomes";
import { webPath } from "@/lib/web-dashboard";

export type FirstEvent = "waiting" | "received" | "refused";

// whether a site's tag works, from the gateway's ingest counts: counted
// events mean it does; only rejections mean the tag runs but its events are
// refused
export function firstEvent(...answers: (Ingest | undefined)[]): FirstEvent {
  let counted = 0;
  let rejected = 0;
  for (const a of answers) {
    if (!a) continue;
    const t = a.totals;
    counted += (t.accepted ?? 0) + (t.quarantined ?? 0) + (t.bot ?? 0);
    rejected += sumRejected(t);
  }
  return counted > 0 ? "received" : rejected > 0 ? "refused" : "waiting";
}

// whether the web dashboard's engine holds a page view of the site
export function webSeen(h: WebHourly | undefined): boolean {
  return (h?.points ?? []).some((p) => p.pageviews > 0);
}

// the first event completes on whichever source sees it first: the gateway
// folds its counts every few minutes, the engine shows a page view at once.
// null while neither has answered.
export function combineFirstEvent(
  gateway: FirstEvent | null,
  web: boolean | null,
): FirstEvent | null {
  if (gateway === "received" || web === true) return "received";
  if (gateway === null) return web === null ? null : "waiting";
  return gateway;
}

export type StepKey = "install" | "event" | "domain" | "toml";
// done; the step to do now; a later step; a step that failed and needs action
export type StepState = "done" | "current" | "todo" | "attention";
export type SetupStep = { key: StepKey; title: string; state: StepState; optional: boolean };

function domainDone(site: Site): boolean {
  return site.verification === "verified";
}

export function setupSteps(site: Site, event: FirstEvent | null): SetupStep[] {
  const received = event === "received";
  const toml = site.stellar_toml;
  return [
    {
      key: "install",
      title: "Install the tag",
      state: received ? "done" : "current",
      optional: false,
    },
    {
      key: "event",
      title: "First event",
      state: received ? "done" : event === "refused" ? "attention" : "todo",
      optional: false,
    },
    {
      key: "domain",
      title: "Verify the domain",
      // a later step stays neutral until the steps before it are done: a
      // failed check shows only once the step is the one to do
      state: domainDone(site)
        ? "done"
        : !received
          ? "todo"
          : site.last_verify_error
            ? "attention"
            : "current",
      optional: false,
    },
    {
      key: "toml",
      title: "stellar.toml",
      state: !toml ? "todo" : toml.status === "found" ? "done" : received ? "attention" : "todo",
      optional: true,
    },
  ];
}

// the required steps done, of all required steps
export function setupProgress(steps: SetupStep[]): { done: number; total: number } {
  const required = steps.filter((s) => !s.optional);
  return { done: required.filter((s) => s.state === "done").length, total: required.length };
}

// the required steps done (site added counts as the first), for summaries
// that count three: added, first event, domain
export function setupDone(site: Site, event: FirstEvent | null): number {
  return 1 + (event === "received" ? 1 : 0) + (domainDone(site) ? 1 : 0);
}

// one status word per site
export function setupStatus(site: Site, event: FirstEvent | null): SetupStatus {
  if (event === null) return "checking";
  if (event === "refused") return "refused";
  if (event === "waiting") return "waiting";
  if (domainDone(site)) return "done";
  return site.last_verify_error ? "failed" : "unverified";
}

// the sentence under the setup header: what to do next
export function nextText(status: SetupStatus): string {
  switch (status) {
    case "checking":
      return "Checking for events.";
    case "waiting":
      return "Next: install the tag, then open the site.";
    case "refused":
      return "Next: fix why events are refused.";
    case "unverified":
      return "Next: verify the domain.";
    case "failed":
      return "Next: fix the domain check.";
    case "done":
      return "Setup is complete.";
  }
}

export type NextAction = { label: string; to: string };

// the one next action of a site in a list
export function nextAction(site: Site, status: SetupStatus): NextAction {
  const setup = siteSetupPath(site);
  switch (status) {
    case "checking":
    case "waiting":
    case "refused":
      return { label: "Continue setup", to: setup };
    case "unverified":
    case "failed":
      return { label: "Verify domain", to: `${setup}#domain` };
    case "done":
      return { label: "Open web data", to: webPath(site.tenant_id, site.id) };
  }
}

// the order of the setup column when sorted: the sites that need the most
// work first
const RANK: Record<SetupStatus, number> = {
  refused: 0,
  failed: 1,
  waiting: 2,
  checking: 3,
  unverified: 4,
  done: 5,
};

export function setupRank(status: SetupStatus): number {
  return RANK[status];
}

export type SetupNext = {
  site: Site;
  step: "install" | "refused" | "verify";
  // of the three steps: site added, first event, domain verified
  done: number;
};

// the one setup step to do next, of the first active site that is not set
// up, for the overview. a site in review waits for an operator, not for its
// project. null when every site is set up, or while an answer is missing.
export function nextSetupStep(
  sites: Site[],
  eventOf: (s: Site) => FirstEvent | null,
): SetupNext | null {
  for (const site of sites) {
    if (site.status !== "active" || site.in_review) continue;
    const event = eventOf(site);
    const status = setupStatus(site, event);
    if (status === "checking") return null;
    if (status === "done") continue;
    const step = status === "waiting" ? "install" : status === "refused" ? "refused" : "verify";
    return { site, step, done: setupDone(site, event) };
  }
  return null;
}

// a site's own pages: its setup and its settings
export const siteSetupPath = (s: Pick<Site, "id" | "tenant_id">) =>
  `/projects/${s.tenant_id}/sites/${s.id}`;
export const siteSettingsPath = (s: Pick<Site, "id" | "tenant_id">) =>
  `/projects/${s.tenant_id}/sites/${s.id}/settings`;
