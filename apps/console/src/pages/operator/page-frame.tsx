// the parts of each operator page that are known before its data: the title
// line and the shape of its content. the operator gate draws them while the
// session and the step-up load, and a page draws the same skeleton while its
// own data loads, so nothing moves when the page lands.

import { ALERTS_REFRESH_MS } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
import { LoadingState, Skeleton } from "@/components/states";
import { Skeleton as Bone } from "@/components/ui/skeleton";

// the review queue beside the project in review: identity, numbers,
// evidence and the decision
export function ReviewSkeleton() {
  return (
    <LoadingState label="Loading the review queue">
      <div className="grid gap-4 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
        <div aria-hidden className="flex flex-col gap-2">
          {Array.from({ length: 4 }, (_, i) => (
            <div
              key={i}
              className="flex flex-col gap-2 rounded-lg border border-line bg-raised p-3"
            >
              <Bone className="h-4 w-2/3" />
              <Bone className="h-3 w-1/2" />
            </div>
          ))}
        </div>
        <div className="flex flex-col gap-6 max-lg:hidden">
          <div aria-hidden className="flex flex-col gap-2">
            <Bone className="h-6 w-1/3" />
            <Bone className="h-3 w-1/2" />
          </div>
          <Skeleton.StatRow count={4} />
          <div className="grid gap-4 xl:grid-cols-2">
            <Skeleton.Table rows={5} columns={2} />
            <Bone aria-hidden className="h-48 w-full rounded-lg" />
          </div>
        </div>
      </div>
    </LoadingState>
  );
}

// alerts come in severity groups of cards
export function AlertsSkeleton() {
  return (
    <LoadingState label="Loading the alerts">
      {Array.from({ length: 2 }, (_, g) => (
        <div key={g} aria-hidden className="flex flex-col gap-3">
          <Bone className="h-4 w-48" />
          {Array.from({ length: 2 - g }, (_, i) => (
            <div
              key={i}
              className="flex flex-col gap-2 rounded-lg border border-line bg-raised p-4"
            >
              <Bone className="h-4 w-1/3" />
              <Bone className="h-3 w-2/3" />
              <Bone className="h-3 w-1/4" />
            </div>
          ))}
        </div>
      ))}
    </LoadingState>
  );
}

// the create form above the list of keys
export function KeysSkeleton() {
  return (
    <LoadingState label="Loading the keys">
      <Bone aria-hidden className="h-72 w-full rounded-lg" />
      <div className="flex flex-col gap-3">
        <Bone aria-hidden className="h-5 w-48" />
        <Skeleton.Table rows={3} columns={6} />
      </div>
    </LoadingState>
  );
}

function ProjectsSkeleton() {
  return (
    <LoadingState label="Loading the projects">
      <Bone aria-hidden className="h-(--vs-control) w-full max-w-xl" />
      <Skeleton.Table rows={8} columns={6} />
    </LoadingState>
  );
}

function CompareSkeleton() {
  return (
    <LoadingState label="Loading the comparison">
      <Bone aria-hidden className="h-(--vs-control) w-full max-w-xl" />
      <div className="grid gap-4 lg:grid-cols-2">
        <Skeleton.Chart />
        <Skeleton.Chart />
      </div>
    </LoadingState>
  );
}

function ProjectSkeleton() {
  return (
    <LoadingState label="Loading the project">
      <Skeleton.StatRow count={4} />
      <Skeleton.Table rows={3} columns={4} />
    </LoadingState>
  );
}

function FormSkeleton() {
  return (
    <LoadingState label="Loading the page">
      <Bone aria-hidden className="h-48 w-full max-w-prose rounded-lg" />
      <Skeleton.Table rows={4} columns={4} />
    </LoadingState>
  );
}

function ListSkeleton() {
  return (
    <LoadingState label="Loading the page">
      <Skeleton.Table rows={6} columns={4} />
    </LoadingState>
  );
}

type Frame = {
  title: string;
  description?: string;
  // the header has a freshness line, so its height is kept
  fresh?: boolean;
  body: () => React.ReactNode;
};

// the header of each operator page, as the page itself draws it
const FRAMES: Record<string, Frame> = {
  "": {
    title: "Review",
    description: "Projects that wait for a decision, oldest first.",
    fresh: true,
    body: ReviewSkeleton,
  },
  alerts: {
    title: "Alerts",
    description: `Alerts that fire now. The page checks again every ${ALERTS_REFRESH_MS / 1000} seconds.`,
    body: AlertsSkeleton,
  },
  projects: { title: "Projects", fresh: true, body: ProjectsSkeleton },
  compare: { title: "Compare", fresh: true, body: CompareSkeleton },
  rules: {
    title: "Rules",
    description:
      "Save a traction rule once. See the projects that meet it, with the days that qualify them.",
    body: ListSkeleton,
  },
  settings: {
    title: "Settings",
    description:
      "Settings for every project. Each change asks for a confirmation and goes to the audit log.",
    body: FormSkeleton,
  },
  operators: {
    title: "Operators",
    description:
      "The people with access to the operator area. Operator admins add, suspend and remove operators.",
    body: FormSkeleton,
  },
  keys: {
    title: "Access keys",
    description:
      "Read-only keys for machines and AI assistants. A key reads daily totals and changes nothing.",
    body: KeysSkeleton,
  },
  audit: {
    title: "Audit log",
    description: "Every action of an operator, a project member or the system. Newest first.",
    fresh: true,
    body: ListSkeleton,
  },
};

// the page while the operator area loads: its own title line and skeleton.
// `rest` is the path after /operator
export function OperatorPagePending({ rest }: { rest: string }) {
  const first = rest.split("/")[0] ?? "";
  const frame: Frame =
    first === "projects" && rest.includes("/")
      ? { title: "Project", body: ProjectSkeleton }
      : (FRAMES[first] ?? { title: "Operator area", body: ListSkeleton });
  const Body = frame.body;
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title={frame.title}
        description={frame.description}
        freshness={frame.fresh ? null : undefined}
      />
      <Body />
    </div>
  );
}
