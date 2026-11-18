import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Mark } from "@veristar/brand/mark";
import { transition } from "@veristar/brand/motion";
import { type ThemeChoice, useTheme } from "@veristar/brand/theme";
import {
  Activity,
  BookOpen,
  Check,
  ChevronsUpDown,
  Ellipsis,
  FolderOpen,
  LogOut,
  Monitor,
  Moon,
  Plus,
  ScrollText,
  Search,
  Sun,
  UserRound,
} from "lucide-react";
import { LayoutGroup, motion } from "motion/react";
import { type ReactNode, useId, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { stepUpQuery } from "@/api/operators";
import { operatorAlertsQuery, operatorTenantsQuery, tenantQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { StatusDot, statusEntry } from "@/components/status";
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuRadioGroup,
  MenuRadioItem,
  MenuSeparator,
  MenuTrigger,
} from "@/components/ui/menu";
import { criticalCount } from "@/lib/alerts";
import { isNotFound } from "@/lib/errors";
import { formatCount } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import {
  areaOf,
  HOME_NAV,
  type NavGroup,
  type NavItem,
  OPERATOR_NAV,
  projectNav,
} from "./nav-items";

// the review queue as the review page reads it, so the badge and the page
// share one cached answer
export const REVIEW_QUEUE: TenantListQuery = {
  status: [],
  review: true,
  q: "",
  sort: "created_at",
  order: "asc",
};

// the project the person opened last in this tab
let lastProject: string | null = null;

// from this many projects on, the switcher has a search field
const SWITCHER_SEARCH_FROM = 8;

export function Sidebar({
  me,
  onSignOut,
  onNavigate,
}: {
  me: Me;
  onSignOut: () => void;
  onNavigate?: () => void;
}) {
  const { pathname } = useLocation();
  const area = areaOf(pathname);
  const docs = siblingUrl(window.location.hostname, "docs");
  const status = siblingUrl(window.location.hostname, "status");
  // the desktop sidebar and the phone drawer are two instances; each needs
  // its own group, or their current-item markers would crossfade
  const group = useId();
  // a project that answers 404 (removed, or not the person's) gets the
  // shell without a project: no switcher name and no project pages
  const asked = area.kind === "project" ? area.id : null;
  const tenant = useQuery({ ...tenantQuery(asked ?? ""), enabled: asked !== null });
  const projectId = asked && !isNotFound(tenant.error) ? asked : null;
  if (projectId) lastProject = projectId;
  // the account page keeps the project the person came from, as a global
  // page should not drop the project context
  const kept =
    area.kind === "home" &&
    area.rest === "account" &&
    lastProject &&
    me.tenants.some((t) => t.id === lastProject)
      ? lastProject
      : null;
  const navProject = projectId ?? kept;
  return (
    <LayoutGroup id={group}>
      <div className="flex h-full flex-col gap-4 overflow-y-auto px-3 py-4">
        <Link
          to="/"
          onClick={onNavigate}
          aria-label="Veristar, your projects"
          className="flex items-center gap-2 self-start rounded-md px-2 py-1 text-fg focus-ring"
        >
          <Mark className="size-5" />
          <span className="text-title">Veristar</span>
        </Link>

        <ProjectSwitcher me={me} id={navProject} onNavigate={onNavigate} />

        {navProject ? (
          <Groups
            label="Project"
            groups={projectNav(navProject)}
            rest={projectId ? area.rest : null}
            onNavigate={onNavigate}
          />
        ) : (
          <nav aria-label="Console" className="flex flex-col gap-px">
            {HOME_NAV.filter((i) => i.key === "projects").map((i) => (
              <Item
                key={i.key}
                item={i}
                current={area.kind === "home" && i.match(area.rest)}
                onNavigate={onNavigate}
              />
            ))}
          </nav>
        )}

        {me.user.is_operator ? (
          <OperatorGroups
            rest={area.kind === "operator" ? area.rest : null}
            onNavigate={onNavigate}
          />
        ) : null}

        <div className="mt-auto flex flex-col gap-px border-t border-line pt-3">
          {docs ? (
            <FooterLink href={docs} icon={<BookOpen aria-hidden />}>
              Docs
            </FooterLink>
          ) : null}
          {status ? (
            <FooterLink href={status} icon={<Activity aria-hidden />}>
              Service status
            </FooterLink>
          ) : null}
          <FooterLink to="/terms" icon={<ScrollText aria-hidden />}>
            Terms
          </FooterLink>
          <FooterLink
            to="/account"
            icon={<UserRound aria-hidden />}
            current={area.kind === "home" && area.rest === "account"}
            onNavigate={onNavigate}
          >
            Account
          </FooterLink>
          <AccountMenu me={me} onSignOut={onSignOut} onNavigate={onNavigate} />
        </div>
      </div>
    </LayoutGroup>
  );
}

function AccountMenu({
  me,
  onSignOut,
  onNavigate,
}: {
  me: Me;
  onSignOut: () => void;
  onNavigate?: () => void;
}) {
  const navigate = useNavigate();
  const theme = useTheme();
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          type="button"
          className="mt-1 flex items-center gap-2 rounded-md px-2 py-2 text-left text-fg transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring aria-expanded:bg-selected max-lg:min-h-(--vs-control-touch)"
        >
          <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-line-strong text-caption font-semibold text-fg">
            {me.user.display_name.slice(0, 2)}
          </span>
          <span className="min-w-0 flex-1 truncate text-small">{me.user.display_name}</span>
          <Ellipsis aria-hidden className="size-4 text-fg-3" />
          <span className="sr-only">, account menu</span>
        </button>
      </MenuTrigger>
      <MenuContent side="top" align="start" className="w-(--radix-dropdown-menu-trigger-width)">
        <MenuLabel>Signed in as {me.user.display_name}</MenuLabel>
        <MenuItem
          onSelect={() => {
            onNavigate?.();
            navigate("/account");
          }}
        >
          <UserRound aria-hidden />
          Account
        </MenuItem>
        <MenuSeparator />
        <MenuLabel>Theme</MenuLabel>
        <MenuRadioGroup
          value={theme.choice}
          onValueChange={(v) => theme.setChoice(v as ThemeChoice)}
        >
          <MenuRadioItem value="system">
            <Monitor aria-hidden />
            System
          </MenuRadioItem>
          <MenuRadioItem value="light">
            <Sun aria-hidden />
            Light
          </MenuRadioItem>
          <MenuRadioItem value="dark">
            <Moon aria-hidden />
            Dark
          </MenuRadioItem>
        </MenuRadioGroup>
        <MenuSeparator />
        <MenuItem onSelect={onSignOut}>
          <LogOut aria-hidden />
          Sign out
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

// the project in view and the way to the others. with many projects the
// menu has a search field.
function ProjectSwitcher({
  me,
  id,
  onNavigate,
}: {
  me: Me;
  id: string | null;
  onNavigate?: () => void;
}) {
  const tenant = useQuery({ ...tenantQuery(id ?? ""), enabled: id !== null });
  const navigate = useNavigate();
  const [q, setQ] = useState("");
  const listed = id ? me.tenants.find((t) => t.id === id) : undefined;
  const name = id ? (tenant.data?.name ?? listed?.name ?? "Project") : "Choose a project";
  const st = id ? (tenant.data?.status ?? listed?.status) : undefined;
  const word = st ? statusEntry({ kind: "project", status: st }) : null;
  const shown = me.tenants.filter((t) => t.name.toLowerCase().includes(q.trim().toLowerCase()));
  const go = (to: string) => {
    navigate(to);
    onNavigate?.();
  };
  return (
    <Menu
      onOpenChange={(o) => {
        if (o) setQ("");
      }}
    >
      <MenuTrigger asChild>
        <button
          type="button"
          className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 rounded-md border border-line bg-raised p-2 text-left text-fg transition-colors duration-(--vs-duration-press) hover:border-line-strong hover:bg-hover focus-ring aria-expanded:border-line-strong max-lg:min-h-(--vs-control-touch)"
        >
          <span className="flex size-7 items-center justify-center rounded-md bg-selected text-caption font-semibold">
            {id ? name.slice(0, 1) : <FolderOpen aria-hidden className="size-4" />}
          </span>
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-body font-semibold">{name}</span>
            {word ? (
              <span className="flex items-center gap-1 text-caption font-normal text-fg-2">
                <StatusDot tone={word.tone} />
                {word.word}
              </span>
            ) : null}
          </span>
          <ChevronsUpDown aria-hidden className="size-4 text-fg-3" />
          <span className="sr-only">, switch project</span>
        </button>
      </MenuTrigger>
      <MenuContent className="w-(--radix-dropdown-menu-trigger-width) min-w-60">
        {me.tenants.length >= SWITCHER_SEARCH_FROM ? (
          <div className="flex items-center gap-2 border-b border-line px-2 pb-1">
            <Search aria-hidden className="size-4 shrink-0 text-fg-3" />
            <input
              aria-label="Search projects"
              placeholder="Search projects"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              // the menu moves focus by typed letters; the field keeps them
              onKeyDown={(e) => e.stopPropagation()}
              className="h-8 min-w-0 flex-1 bg-transparent text-body text-fg outline-none placeholder:text-fg-3"
            />
          </div>
        ) : null}
        <MenuLabel>Your projects</MenuLabel>
        {shown.map((t) => (
          <MenuItem key={t.id} onSelect={() => go(`/projects/${t.id}`)}>
            <StatusDot tone={statusEntry({ kind: "project", status: t.status }).tone} />
            <span className="min-w-0 flex-1 truncate">{t.name}</span>
            {t.id === id ? <Check aria-hidden /> : null}
          </MenuItem>
        ))}
        {shown.length === 0 ? (
          <p className="px-2 py-2 text-small text-fg-2">
            {me.tenants.length === 0 ? "No project yet." : "No project matches."}
          </p>
        ) : null}
        <MenuSeparator />
        <MenuItem onSelect={() => go("/")}>
          <FolderOpen aria-hidden />
          All projects
        </MenuItem>
        <MenuItem onSelect={() => go("/projects/new")}>
          <Plus aria-hidden />
          Enroll a project
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

function Groups({
  label,
  groups,
  rest,
  onNavigate,
  badges,
}: {
  label: string;
  groups: NavGroup[];
  rest: string | null;
  onNavigate?: () => void;
  badges?: Record<string, ReactNode>;
}) {
  return (
    <nav aria-label={label} className="flex flex-col gap-3">
      {groups.map((g) => (
        <div key={g.key} className="flex flex-col gap-px">
          <span className="px-2 pb-1 text-caption text-fg-2">{g.label}</span>
          {g.items.map((n) => (
            <Item
              key={n.key}
              item={n}
              current={rest !== null && n.match(rest)}
              onNavigate={onNavigate}
              badge={badges?.[n.key]}
            />
          ))}
        </div>
      ))}
    </nav>
  );
}

// the operator groups. the review and alert badges are read only inside
// the operator area and once the session holds a step-up (contract 5.13,
// 5.15): before it every operator route answers step_up_required
function OperatorGroups({ rest, onNavigate }: { rest: string | null; onNavigate?: () => void }) {
  const inOperator = rest !== null;
  const stepUp = useQuery({ ...stepUpQuery, enabled: inOperator });
  const live = inOperator && stepUp.data?.stepped_up === true;
  const alerts = useQuery({ ...operatorAlertsQuery, enabled: live });
  const queue = useInfiniteQuery({ ...operatorTenantsQuery(REVIEW_QUEUE), enabled: live });
  const current = live && !alerts.error ? alerts.data : undefined;
  const critical = current ? criticalCount(current.alerts) : 0;
  const count = current?.alerts.length ?? 0;
  const failed = live && alerts.error !== null;
  const first = queue.data?.pages[0];
  const waiting = first ? first.items.length : 0;
  const more = first ? first.next_cursor !== null : false;
  return (
    <Groups
      label="Operator"
      groups={OPERATOR_NAV}
      rest={rest}
      onNavigate={onNavigate}
      badges={{
        review:
          waiting > 0 ? (
            <Badge tone="neutral">
              {formatCount(waiting)}
              {more ? "+" : ""}
              <span className="sr-only">
                {waiting === 1 && !more ? " project waits" : " projects wait"} for review
              </span>
            </Badge>
          ) : null,
        alerts:
          failed || count > 0 ? (
            <Badge tone={failed || critical > 0 ? "critical" : "warning"}>
              {failed ? "!" : formatCount(count)}
              <span className="sr-only">
                {failed
                  ? ", alert source unreachable"
                  : count === 1
                    ? " alert firing"
                    : " alerts firing"}
              </span>
            </Badge>
          ) : null,
      }}
    />
  );
}

function Badge({
  tone,
  children,
}: {
  tone: "neutral" | "warning" | "critical";
  children: ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex h-5 min-w-5 items-center justify-center rounded-full px-1 font-mono text-axis tabular",
        tone === "critical"
          ? "bg-critical-solid text-on-critical"
          : tone === "warning"
            ? "border border-warning-line text-warning"
            : "bg-selected text-fg",
      )}
    >
      {children}
    </span>
  );
}

function Item({
  item,
  current,
  badge,
  onNavigate,
}: {
  item: NavItem;
  current: boolean;
  badge?: ReactNode;
  onNavigate?: () => void;
}) {
  const Icon = item.icon;
  return (
    <Link
      to={item.to}
      onClick={onNavigate}
      aria-current={current ? "page" : undefined}
      className={cn(
        "relative flex h-8 items-center gap-2 rounded-md px-2 text-body transition-colors duration-(--vs-duration-press) ease-standard focus-ring active:bg-selected max-lg:h-(--vs-control-touch)",
        current ? "font-medium text-fg" : "text-fg-2 hover:bg-hover hover:text-fg",
      )}
    >
      {current ? (
        <motion.span
          layoutId="sidebar-current"
          transition={transition.layout}
          aria-hidden
          className="absolute inset-0 rounded-md bg-selected"
        />
      ) : null}
      <Icon aria-hidden className="relative size-4 shrink-0" />
      <span className="relative min-w-0 flex-1 truncate">{item.label}</span>
      {badge ? <span className="relative">{badge}</span> : null}
    </Link>
  );
}

function FooterLink({
  href,
  to,
  icon,
  current,
  onNavigate,
  children,
}: {
  href?: string;
  to?: string;
  icon: ReactNode;
  current?: boolean;
  onNavigate?: () => void;
  children: ReactNode;
}) {
  const cls = cn(
    "flex h-8 items-center gap-2 rounded-md px-2 text-small transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring [&_svg]:size-4 max-lg:h-(--vs-control-touch)",
    current ? "bg-selected text-fg" : "text-fg-2",
  );
  return to ? (
    <Link to={to} onClick={onNavigate} aria-current={current ? "page" : undefined} className={cls}>
      {icon}
      {children}
    </Link>
  ) : (
    <a href={href} target="_blank" rel="noopener" className={cls}>
      {icon}
      {children}
    </a>
  );
}
