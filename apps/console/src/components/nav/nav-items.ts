import {
  Activity,
  BarChart3,
  Blocks,
  Bot,
  FileSearch,
  FolderKanban,
  FolderOpen,
  Gauge,
  Globe,
  Inbox,
  KeyRound,
  LayoutGrid,
  ListChecks,
  type LucideIcon,
  MonitorSmartphone,
  Plug,
  ScrollText,
  Server,
  Settings,
  ShieldCheck,
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
          icon: FileSearch,
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
          icon: MonitorSmartphone,
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
          icon: ShieldCheck,
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
        label: "Projects",
        to: "/operator/projects",
        icon: FolderKanban,
        match: (r) => under(r, "projects"),
      },
      {
        key: "compare",
        label: "Compare",
        to: "/operator/compare",
        icon: BarChart3,
        match: (r) => under(r, "compare"),
      },
      {
        key: "rules",
        label: "Rules",
        to: "/operator/rules",
        icon: ListChecks,
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
        icon: Gauge,
        match: (r) => under(r, "operators"),
      },
      {
        key: "keys",
        label: "Access keys",
        to: "/operator/keys",
        icon: KeyRound,
        match: (r) => under(r, "keys"),
      },
      {
        key: "audit",
        label: "Audit log",
        to: "/operator/audit",
        icon: ScrollText,
        match: (r) => under(r, "audit"),
      },
    ],
  },
];

// the pages outside a project and the operator area
export const HOME_NAV: NavItem[] = [
  {
    key: "projects",
    label: "Projects",
    to: "/",
    icon: FolderOpen,
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

// the on-chain and scf pages still pass this to a tab bar; the sidebar
// lists every page now, so there is no second level to draw
export function projectTabs(_id: string, _place: string): null {
  return null;
}
