// the parts of each project page that are known before its data: the title
// line and the shape of its content. a page draws its header from here and
// its skeleton while its data loads; the project frame draws the same two
// while the project itself loads, so nothing moves when the page lands.

import { ArrowLeft } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { areaOf } from "@/components/nav/nav-items";
import { PageHeader } from "@/components/page-header";
import { LoadingState, Skeleton } from "@/components/states";
import { Skeleton as Bone } from "@/components/ui/skeleton";

export const PAGE_HEADERS = {
  overview: {
    title: "Overview",
    description: "The main numbers of your project: web visits, API traffic and on-chain activity.",
  },
  web: { title: "Web", description: "Visits to your sites: pages, sources, places and devices." },
  api: {
    title: "API",
    description: "Requests to your API: volume, errors and latency by service and route.",
  },
  onchain: {
    title: "On-chain",
    description:
      "The Stellar accounts and Soroban contracts of your project, and their ledger activity.",
  },
  scf: {
    title: "SCF review",
    description: "What SCF reviewers see about your project. They read the same figures.",
  },
  sites: { title: "Sites", description: "Each website of the project and how far its setup is." },
  "connect-api": {
    title: "Connect your API",
    description:
      "Add a second exporter to your app. It sends request counts, status codes and latency here.",
  },
  ai: {
    title: "AI assistants",
    description:
      "Ask Claude, ChatGPT, Cursor or another MCP client about this project. A connection only reads data, and only of this project.",
  },
  members: { title: "Members", description: "The people who manage this project." },
  privacy: {
    title: "Privacy",
    description:
      "When a visitor asks you to erase their data, request it here. The audit log records each request.",
  },
} as const;

type PageKey = keyof typeof PAGE_HEADERS;

// the pages whose header has a freshness line: their pending header keeps
// the line's height, so the page does not move when it lands
const FRESHNESS = new Set<PageKey>(["overview", "web", "api", "onchain", "sites"]);

// ---- the skeleton of each page, in the shape of what lands

export function OverviewSkeleton() {
  return (
    <LoadingState label="Loading the overview">
      <Bone className="h-(--vs-control) w-72 max-w-full" />
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
          {Array.from({ length: 6 }, (_, i) => (
            <div
              key={i}
              className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4"
            >
              <Bone className="h-3 w-1/2" />
              <Bone className="h-7 w-3/4" />
              <Bone className="h-3 w-2/5" />
            </div>
          ))}
        </div>
        <Bone className="h-4 w-32" />
        <Skeleton.Chart />
      </div>
      <SummarySkeleton />
    </LoadingState>
  );
}

// the three summary cards under the overview's numbers
export function SummarySkeleton() {
  return (
    <div aria-hidden className="grid gap-3 md:grid-cols-3">
      {Array.from({ length: 3 }, (_, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4">
          <Bone className="h-3 w-1/3" />
          <Bone className="h-6 w-2/3" />
          <Bone className="h-3 w-1/4" />
        </div>
      ))}
    </div>
  );
}

export function WebSkeleton() {
  return (
    <LoadingState label="Loading the web numbers">
      <Skeleton.StatRow count={5} />
      <Skeleton.Chart />
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton.Table rows={5} columns={2} />
        <Skeleton.Table rows={5} columns={2} />
      </div>
    </LoadingState>
  );
}

export function ApiSkeleton() {
  return (
    <LoadingState label="Loading the API numbers">
      <Skeleton.StatRow count={4} />
      <Skeleton.Chart />
      <Skeleton.Table rows={6} columns={7} />
    </LoadingState>
  );
}

export function OnchainSkeleton() {
  return (
    <LoadingState label="Loading on-chain activity">
      <Skeleton.StatRow count={4} />
      <Skeleton.Chart bars={30} />
      <Skeleton.Table rows={2} columns={3} />
    </LoadingState>
  );
}

export function ScfSkeleton() {
  return (
    <LoadingState label="Loading what SCF reviewers see">
      <Skeleton.StatRow count={3} />
      <Skeleton.Table rows={5} columns={5} />
    </LoadingState>
  );
}

export function SitesSkeleton() {
  return (
    <LoadingState label="Loading sites">
      <Skeleton.Table rows={4} columns={5} />
    </LoadingState>
  );
}

export function MembersSkeleton() {
  return (
    <LoadingState label="Loading members">
      <Skeleton.Table rows={4} columns={4} />
    </LoadingState>
  );
}

export function PrivacySkeleton() {
  return (
    <LoadingState label="Loading the privacy page">
      <Bone className="h-64 w-full" />
      <Skeleton.Table rows={3} columns={4} />
    </LoadingState>
  );
}

// connect-api and ai draw their content without a request; while the
// project loads they show the first block of it
function GuideSkeleton() {
  return (
    <LoadingState label="Loading the page">
      <Bone className="h-40 w-full max-w-content" />
      <Bone className="h-24 w-full max-w-content" />
    </LoadingState>
  );
}

// ---- the site pages: a link back to Sites over the site's name

export function SitesBackLink({ tenantId }: { tenantId: string }) {
  return (
    <Link
      to={`/projects/${tenantId}/sites`}
      className="inline-flex w-fit items-center gap-1 rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring"
    >
      <ArrowLeft aria-hidden className="size-4" />
      Sites
    </Link>
  );
}

// a site page before its site is known: the title says which page it is
export function SitePageLoading({
  tenantId,
  page,
}: {
  tenantId: string;
  page: "setup" | "settings";
}) {
  return (
    <div className="flex flex-col gap-6">
      <SitesBackLink tenantId={tenantId} />
      <PageHeader title={page === "setup" ? "Site setup" : "Site settings"} />
      <SiteBodySkeleton page={page} />
    </div>
  );
}

function SiteBodySkeleton({ page }: { page: "setup" | "settings" }) {
  return page === "setup" ? (
    <div role="status" aria-busy="true" className="flex flex-col gap-4">
      <span className="sr-only">Loading the site</span>
      <Bone className="h-2 w-48" />
      <Bone className="h-40 w-full" />
      <Bone className="h-24 w-full" />
    </div>
  ) : (
    <div role="status" aria-busy="true" className="flex max-w-prose flex-col gap-4">
      <span className="sr-only">Loading the site</span>
      <Bone className="h-40 w-full" />
      <Bone className="h-24 w-full" />
    </div>
  );
}

// the api page draws its filters before its numbers load
function ApiPending() {
  return (
    <>
      <Bone aria-hidden className="h-(--vs-control) w-full max-w-xl" />
      <ApiSkeleton />
    </>
  );
}

const SKELETON: Record<PageKey, () => ReactNode> = {
  overview: OverviewSkeleton,
  web: WebSkeleton,
  api: ApiPending,
  onchain: OnchainSkeleton,
  scf: ScfSkeleton,
  sites: SitesSkeleton,
  "connect-api": GuideSkeleton,
  ai: GuideSkeleton,
  members: MembersSkeleton,
  privacy: PrivacySkeleton,
};

// which page of a project a path is, from the part after /projects/:id
export function projectPageOf(
  pathname: string,
): { key: PageKey } | { site: "setup" | "settings" } | null {
  const a = areaOf(pathname);
  if (a.kind !== "project") return null;
  const parts = a.rest.split("/");
  if (parts[0] === "sites" && parts[1])
    return { site: parts[2] === "settings" ? "settings" : "setup" };
  const key = (parts[0] === "" ? "overview" : parts[0]) as PageKey;
  return key in PAGE_HEADERS ? { key } : null;
}

// the page while the project loads: its own title line and skeleton
export function ProjectPagePending({ tenantId, pathname }: { tenantId: string; pathname: string }) {
  const page = projectPageOf(pathname);
  if (!page)
    return (
      <LoadingState label="Loading the project">
        <Skeleton.Table rows={3} />
      </LoadingState>
    );
  if ("site" in page) return <SitePageLoading tenantId={tenantId} page={page.site} />;
  const Body = SKELETON[page.key];
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <PageHeader
        {...PAGE_HEADERS[page.key]}
        freshness={FRESHNESS.has(page.key) ? null : undefined}
      />
      <Body />
    </div>
  );
}
