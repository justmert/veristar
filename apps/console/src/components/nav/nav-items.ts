import {
  Activity,
  BarChart3,
  Blocks,
  Bot,
  FileCheck2,
  FileText,
  Folder,
  Globe,
  Inbox,
  Key,
  LayoutGrid,
  List,
  type LucideIcon,
  Monitor,
  Plug,
  Server,
  Settings,
  Shield,
  ShieldMinus,
  UserRound,
  Users,
} from "lucide-react";

export type NavItem = {
  key: string;
  label: string;
  to: string;
  icon: LucideIcon;
  // the item is current for these paths (relative to the project or the
  // operator root), sub-pages included: a site page keeps Sites current
  match: (rest: string) => boolean;
  // reached from the command palette and from other pages, not listed in
  // the sidebar
  paletteOnly?: boolean;
};

export type NavGroup = { key: string; label: string; items: NavItem[] };

const under = (r: string, p: string) => r === p || r.startsWith(`${p}/`);

// the project area: every page of a project, in four groups. no second
// level: each page is one item here.
export function projectNav(id: string): NavGroup[] {
  const base = `/projects/${id}`;
  return [
    {
      key: "data",
      label: "Data",
      items: [
        { key: "overview", label: "Overview", to: base, icon: LayoutGrid, match: (r) => r === "" },
        { key: "web", label: "Web", to: `${base}/web`, icon: Globe, match: (r) => under(r, "web") },
        {
          key: "api",
          label: "API",
          to: `${base}/api`,
          icon: Server,
          match: (r) => under(r, "api"),
        },
        {
          key: "onchain",
          label: "On-chain",
          to: `${base}/onchain`,
          icon: Blocks,
          match: (r) => under(r, "onchain"),
        },
      ],
    },
    {
      key: "funding",
      label: "Funding",
      items: [
        {
          key: "scf",
          label: "SCF review",
          to: `${base}/scf`,
          icon: FileCheck2,
          match: (r) => under(r, "scf"),
        },
      ],
    },
    {
      key: "setup",
      label: "Setup",
      items: [
        {
          key: "sites",
          label: "Sites",
          to: `${base}/sites`,
          icon: Monitor,
          match: (r) => under(r, "sites"),
        },
        {
          key: "connect-api",
          label: "Connect API",
          to: `${base}/connect-api`,
          icon: Plug,
          match: (r) => under(r, "connect-api"),
        },
        {
          key: "ai",
          label: "AI assistants",
          to: `${base}/ai`,
          icon: Bot,
          match: (r) => under(r, "ai"),
        },
      ],
    },
    {
      key: "team",
      label: "Team",
      items: [
        {
          key: "members",
          label: "Members",
          to: `${base}/members`,
          icon: Users,
          match: (r) => under(r, "members"),
        },
        {
          key: "privacy",
          label: "Privacy",
          to: `${base}/privacy`,
          icon: Shield,
          match: (r) => under(r, "privacy"),
        },
      ],
    },
  ];
}

// the operator area, for operators only. review and alerts carry badges.
export const OPERATOR_NAV: NavGroup[] = [
  {
    key: "work",
    label: "Work",
    items: [
      { key: "review", label: "Review", to: "/operator", icon: Inbox, match: (r) => r === "" },
      {
        key: "alerts",
        label: "Alerts",
        to: "/operator/alerts",
        icon: Activity,
        match: (r) => under(r, "alerts"),
      },
      {
        key: "projects",
        label: "All projects",
        to: "/operator/projects",
        icon: Folder,
        match: (r) => under(r, "projects"),
      },
      {
        key: "compare",
        label: "Compare",
        to: "/operator/compare",
        icon: BarChart3,
        match: (r) => under(r, "compare"),
        paletteOnly: true,
      },
      {
        key: "rules",
        label: "Rules",
        to: "/operator/rules",
        icon: List,
        match: (r) => under(r, "rules"),
      },
    ],
  },
  {
    key: "admin",
    label: "Admin",
    items: [
      {
        key: "settings",
        label: "Settings",
        to: "/operator/settings",
        icon: Settings,
        match: (r) => under(r, "settings"),
      },
      {
        key: "operators",
        label: "Operators",
        to: "/operator/operators",
        icon: ShieldMinus,
        match: (r) => under(r, "operators"),
      },
      {
        key: "keys",
        label: "Access keys",
        to: "/operator/keys",
        icon: Key,
        match: (r) => under(r, "keys"),
      },
      {
        key: "audit",
        label: "Audit log",
        to: "/operator/audit",
        icon: FileText,
        match: (r) => under(r, "audit"),
      },
    ],
  },
];

// the pages outside a project and the operator area
export const HOME_NAV: NavItem[] = [
  {
    key: "projects",
    label: "My projects",
    to: "/",
    icon: Folder,
    match: (r) => r === "",
  },
  {
    key: "account",
    label: "Account",
    to: "/account",
    icon: UserRound,
    match: (r) => under(r, "account"),
  },
];

export type Area =
  | { kind: "project"; id: string; rest: string }
  | { kind: "operator"; rest: string }
  | { kind: "home"; rest: string };

// where a path is: a project, the operator area or the pages around them
export function areaOf(pathname: string): Area {
  const p = pathname.match(/^\/projects\/([^/]+)(?:\/(.*))?$/);
  if (p?.[1] && p[1] !== "new") return { kind: "project", id: p[1], rest: trimSlash(p[2] ?? "") };
  const o = pathname.match(/^\/operator(?:\/(.*))?$/);
  if (o) return { kind: "operator", rest: trimSlash(o[1] ?? "") };
  return { kind: "home", rest: trimSlash(pathname.replace(/^\//, "")) };
}

function trimSlash(r: string): string {
  return r.replace(/\/+$/, "");
}

// the current nav item of a path, if one is current
export function currentItem(pathname: string): NavItem | null {
  const a = areaOf(pathname);
  const items =
    a.kind === "project"
      ? projectNav(a.id).flatMap((g) => g.items)
      : a.kind === "operator"
        ? OPERATOR_NAV.flatMap((g) => g.items)
        : HOME_NAV;
  return items.find((i) => i.match(a.rest)) ?? null;
}

// the name of the page, for the phone top bar and the command palette
export function pageTitle(pathname: string): string {
  const a = areaOf(pathname);
  if (a.kind === "home" && a.rest === "projects/new") return "Enroll a project";
  return currentItem(pathname)?.label ?? "Veristar";
}

// the pages of a project and of the operator area that read only routes the
// demo serves. the rest (members, privacy, tokens, assistants, site
// settings, alerts, audit, the roster, settings, access keys, the review
// queue) need a session or exist to change something.
const PROJECT_PAGES: ReadonlySet<string> = new Set([
  "overview",
  "web",
  "api",
  "onchain",
  "scf",
  "sites",
]);
const OPERATOR_PAGES: ReadonlySet<string> = new Set(["projects", "compare", "rules"]);

export function demoNav(groups: NavGroup[], area: "project" | "operator"): NavGroup[] {
  const shown = area === "project" ? PROJECT_PAGES : OPERATOR_PAGES;
  return groups
    .map((g) => ({ ...g, items: g.items.filter((i) => shown.has(i.key)) }))
    .filter((g) => g.items.length > 0);
}

// whether a console path is a page the demo can draw. a site's page is one
// (its settings read as text there), and the old settings address leads to it
export function demoAllows(pathname: string): boolean {
  const a = areaOf(pathname);
  if (a.kind === "project") {
    if (a.rest === "") return true;
    const [first = "", ...more] = a.rest.split("/");
    if (first === "sites") return more.length <= 1 || (more.length === 2 && more[1] === "settings");
    return more.length === 0 && PROJECT_PAGES.has(first);
  }
  if (a.kind === "operator") {
    const [first = "", ...more] = a.rest.split("/");
    if (first === "projects") return more.length <= 1;
    return more.length === 0 && OPERATOR_PAGES.has(first);
  }
  // the project list; enrolling and the account need a person
  return a.rest === "";
}
