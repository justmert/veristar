import {
  Activity,
  BarChart3,
  Blocks,
  Bot,
  FileSignature,
  FolderKanban,
  LayoutGrid,
  type LucideIcon,
  Server,
  ShieldCheck,
  SlidersHorizontal,
} from "lucide-react";

export type NavItem = {
  key: string;
  label: string;
  to: string;
  icon: LucideIcon;
  // the item is current for these path prefixes (relative to the project
  // or the operator root); `exact` matches the root itself only
  match: (rest: string) => boolean;
};

// the project area, six places (system v2 C-Nav). sites live on the
// overview; members and privacy are settings.
export function projectNav(id: string): NavItem[] {
  const base = `/projects/${id}`;
  return [
    {
      key: "overview",
      label: "Overview",
      to: base,
      icon: LayoutGrid,
      match: (r) => r === "" || r.startsWith("sites"),
    },
    {
      key: "api",
      label: "API",
      to: `${base}/api/traffic`,
      icon: Server,
      match: (r) => r.startsWith("api"),
    },
    {
      key: "onchain",
      label: "On-chain",
      to: `${base}/onchain`,
      icon: Blocks,
      match: (r) => r.startsWith("onchain") || r.startsWith("scf-view"),
    },
    {
      key: "reports",
      label: "Signed reports",
      to: `${base}/reports`,
      icon: FileSignature,
      match: (r) => r.startsWith("reports"),
    },
    {
      key: "ai",
      label: "AI assistants",
      to: `${base}/ai`,
      icon: Bot,
      match: (r) => r.startsWith("ai"),
    },
    {
      key: "settings",
      label: "Settings",
      to: `${base}/members`,
      icon: SlidersHorizontal,
      match: (r) => r.startsWith("members") || r.startsWith("privacy"),
    },
  ];
}

export const OPERATOR_NAV: NavItem[] = [
  {
    key: "attention",
    label: "Attention",
    to: "/operator",
    icon: Activity,
    match: (r) => r === "" || r.startsWith("alerts"),
  },
  {
    key: "projects",
    label: "Projects",
    to: "/operator/tenants",
    icon: FolderKanban,
    match: (r) => r.startsWith("tenants") || r === "web" || r === "api" || r.startsWith("rules"),
  },
  {
    key: "compare",
    label: "Compare",
    to: "/operator/compare",
    icon: BarChart3,
    match: (r) => r.startsWith("compare"),
  },
  {
    key: "admin",
    label: "Admin",
    to: "/operator/settings",
    icon: ShieldCheck,
    match: (r) => ["settings", "operators", "api-keys", "ai", "audit"].some((p) => r.startsWith(p)),
  },
];

// the second level inside a place, drawn as tabs under the page header
export function projectTabs(id: string, place: string) {
  const base = `/projects/${id}`;
  if (place === "api")
    return [
      { to: `${base}/api/traffic`, label: "Traffic" },
      { to: `${base}/api`, label: "Connect" },
    ];
  if (place === "onchain")
    return [
      { to: `${base}/onchain`, label: "Accounts and contracts" },
      { to: `${base}/scf-view`, label: "What SCF sees" },
    ];
  if (place === "settings")
    return [
      { to: `${base}/members`, label: "Members" },
      { to: `${base}/privacy`, label: "Privacy" },
    ];
  return null;
}

export const OPERATOR_TABS: Record<string, { to: string; label: string }[]> = {
  attention: [
    { to: "/operator", label: "Review queue" },
    { to: "/operator/alerts", label: "Alerts" },
  ],
  projects: [
    { to: "/operator/tenants", label: "Projects" },
    { to: "/operator/web", label: "Global web" },
    { to: "/operator/api", label: "Global API" },
    { to: "/operator/rules", label: "Rules" },
  ],
  admin: [
    { to: "/operator/settings", label: "Settings" },
    { to: "/operator/operators", label: "Operators" },
    { to: "/operator/api-keys", label: "API keys" },
    { to: "/operator/ai", label: "AI / MCP" },
    { to: "/operator/audit", label: "Audit log" },
  ],
};
