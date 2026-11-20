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
  CircleAlert,
  Ellipsis,
  FileText,
  Folder,
  LogOut,
  Plus,
  Search,
  Shield,
  UserRound,
} from "lucide-react";
import { LayoutGroup, motion } from "motion/react";
import { type ReactNode, useId, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { stepUpQuery } from "@/api/operators";
import { operatorAlertsQuery, operatorTenantsQuery, tenantQuery } from "@/api/queries";
import type { FiringAlert, Me } from "@/api/schemas";
import { Avatar } from "@/components/avatar";
import { Segmented } from "@/components/segmented";
import { CountBadge, StatusDot, statusEntry, toneText } from "@/components/status";
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
} from "@/components/ui/menu";
import { criticalCount } from "@/lib/alerts";
import { useDemo } from "@/lib/demo";
import { isNotFound } from "@/lib/errors";
import { formatCount } from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import { GoToButton } from "./go-to";
import {
  areaOf,
  demoNav,
  HOME_NAV,
  type NavGroup,
  type NavItem,
  OPERATOR_NAV,
  projectNav,
} from "./nav-items";
import { useOperatorAdmin } from "./operator-role";

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
  // the demo lists the pages it can draw and has no account to open
  const demo = useDemo();
  return (
    <LayoutGroup id={group}>
      {/* the brand and the switcher stay at the top and the account at the
          bottom; only the page list between them scrolls */}
      <div className="flex h-full flex-col py-4">
        <div className="flex shrink-0 flex-col px-3">
          <Link
            to="/"
            onClick={onNavigate}
            aria-label="Veristar, your projects"
            className="mb-2 flex items-center gap-2 self-start rounded-md px-2 py-1 text-[15px] font-semibold text-fg focus-ring"
          >
            <Mark className="size-[18px]" />
            Veristar
          </Link>

          {/* the switcher is the one way between contexts: a project, or the
              operator area for an operator. the list below shows the pages of
              that one context only; the command palette reaches every page */}
          <ContextSwitcher
            me={me}
            id={area.kind === "operator" ? null : navProject}
            operatorArea={area.kind === "operator"}
            onNavigate={onNavigate}
          />
          {/* the command palette, also on ctrl k from any page */}
          <GoToButton variant="sidebar" className="mt-1.5" />
        </div>
        <div className="mt-1 flex min-h-0 flex-1 flex-col overflow-y-auto px-3 pb-2">
          {area.kind === "operator" && me.user.is_operator ? (
            <OperatorGroups rest={area.rest} onNavigate={onNavigate} />
          ) : navProject ? (
            <Groups
              label="Project"
              groups={demo ? demoNav(projectNav(navProject), "project") : projectNav(navProject)}
              rest={projectId ? area.rest : null}
              onNavigate={onNavigate}
            />
          ) : (
            <nav aria-label="Console" className="mt-2.5 flex flex-col gap-1">
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
        </div>
        <div className="mx-3 flex shrink-0 flex-col gap-0.5 border-t border-line pt-2.5">
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
          <FooterLink to="/terms" icon={<FileText aria-hidden />}>
            Terms
          </FooterLink>
          {/* one way to Account: the account menu, which also holds the
              theme and sign out. it is marked current on the account page */}
          <ThemeToggle />
          {demo ? null : (
            <AccountMenu
              me={me}
              onSignOut={onSignOut}
              onNavigate={onNavigate}
              current={area.kind === "home" && area.rest === "account"}
            />
          )}
        </div>
      </div>
    </LayoutGroup>
  );
}

// the theme: system, light or dark, as three words
function ThemeToggle() {
  const theme = useTheme();
  return (
    <Segmented
      label="Theme"
      value={theme.choice}
      onChange={(v) => theme.setChoice(v as ThemeChoice)}
      className="mx-2 mt-2 mb-1.5"
      options={[
        { value: "system", label: "System" },
        { value: "light", label: "Light" },
        { value: "dark", label: "Dark" },
      ]}
    />
  );
}

function AccountMenu({
  me,
  onSignOut,
  onNavigate,
  current,
}: {
  me: Me;
  onSignOut: () => void;
  onNavigate?: () => void;
  current?: boolean;
}) {
  const navigate = useNavigate();
  return (
    <Menu>
      <MenuTrigger asChild>
        <button
          type="button"
          aria-current={current ? "page" : undefined}
          className="flex items-center gap-2.5 rounded-[8px] px-2 py-1.5 text-left text-fg transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring aria-expanded:bg-hover aria-[current=page]:bg-hover max-lg:min-h-(--vs-control-touch)"
        >
          <Avatar name={me.user.display_name} className="size-6 border-0 text-axis" />
          <span className="min-w-0 flex-1 truncate text-body">{me.user.display_name}</span>
          <Ellipsis aria-hidden className="size-4 text-fg-3" />
          <span className="sr-only">, Account menu</span>
        </button>
      </MenuTrigger>
      <MenuContent side="top" align="start" className="w-(--radix-dropdown-menu-trigger-width)">
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
        <MenuItem onSelect={onSignOut}>
          <LogOut aria-hidden />
          Sign out
        </MenuItem>
      </MenuContent>
    </Menu>
  );
}

// the context in view (a project, or the operator area) and the way to the
// others. with many projects the menu has a search field.
function ContextSwitcher({
  me,
  id,
  operatorArea,
  onNavigate,
}: {
  me: Me;
  id: string | null;
  operatorArea: boolean;
  onNavigate?: () => void;
}) {
  const tenant = useQuery({ ...tenantQuery(id ?? ""), enabled: id !== null });
  const navigate = useNavigate();
  const demo = useDemo();
  const [q, setQ] = useState("");
  const listed = id ? me.tenants.find((t) => t.id === id) : undefined;
  const name = operatorArea
    ? "Operator area"
    : id
      ? (tenant.data?.name ?? listed?.name ?? "Project")
      : "Choose a project";
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
          className="grid min-h-12 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2.5 rounded-lg border border-line bg-sunken px-2.5 py-1.5 text-left text-fg transition-colors duration-(--vs-duration-press) hover:border-line-strong focus-ring aria-expanded:border-line-strong max-lg:min-h-(--vs-control-touch)"
        >
          <span className="flex size-[26px] items-center justify-center rounded-md bg-selected text-caption font-semibold">
            {operatorArea ? (
              "O"
            ) : id ? (
              name.slice(0, 1).toUpperCase()
            ) : (
              <Folder aria-hidden className="size-3.5" />
            )}
          </span>
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-body leading-[18px] font-semibold" title={name}>
              {name}
            </span>
            {word ? (
              <span className="flex items-center gap-[5px] text-caption font-normal text-fg-2">
                <StatusDot tone={word.tone} className="size-1.5" />
                {word.word}
              </span>
            ) : null}
          </span>
          <ChevronsUpDown aria-hidden className="size-3.5 text-fg-3" />
          <span className="sr-only">, switch</span>
        </button>
      </MenuTrigger>
      {/* as wide as the switcher, so it never covers the sidebar's edge */}
      <MenuContent className="w-(--radix-dropdown-menu-trigger-width)">
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
        <MenuLabel>My projects</MenuLabel>
        {shown.map((t) => (
          <MenuItem key={t.id} onSelect={() => go(`/projects/${t.id}`)}>
            <StatusDot tone={statusEntry({ kind: "project", status: t.status }).tone} />
            <span className="min-w-0 flex-1 truncate" title={t.name}>
              {t.name}
            </span>
            {t.id === id ? <Check aria-hidden /> : null}
          </MenuItem>
        ))}
        {shown.length === 0 ? (
          <p className="px-2 py-2 text-small text-fg-2">
            {me.tenants.length === 0 ? "No project yet." : "No project matches."}
          </p>
        ) : null}
        {me.user.is_operator ? (
          <>
            <MenuSeparator />
            <MenuItem onSelect={() => go("/operator")}>
              <Shield aria-hidden />
              <span className="min-w-0 flex-1 truncate">Operator area</span>
              {operatorArea ? <Check aria-hidden /> : null}
            </MenuItem>
          </>
        ) : null}
        <MenuSeparator />
        <MenuItem onSelect={() => go("/")}>
          <Folder aria-hidden />
          My projects
        </MenuItem>
        {demo ? null : (
          <MenuItem onSelect={() => go("/projects/new")}>
            <Plus aria-hidden />
            Enroll a project
          </MenuItem>
        )}
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
    <nav aria-label={label} className="flex flex-col">
      {groups.map((g) => (
        <div key={g.key} className="flex flex-col gap-1">
          <span className="px-2 pt-3.5 text-caption text-fg-3">{g.label}</span>
          {g.items
            .filter((n) => !n.paletteOnly)
            .map((n) => (
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

// the operator groups. the review and alert badges are read only once the
// session holds a step-up (contract 5.13, 5.15): before it every operator
// route answers step_up_required. alerts are platform operations: the item
// and its badge are for operator admins only
function OperatorGroups({ rest, onNavigate }: { rest: string; onNavigate?: () => void }) {
  // the demo has no step-up, no review queue and no alerts to count
  const demo = useDemo();
  const stepUp = useQuery({ ...stepUpQuery, enabled: !demo });
  const live = !demo && stepUp.data?.stepped_up === true;
  const admin = useOperatorAdmin() === true;
  const alerts = useQuery({ ...operatorAlertsQuery, enabled: live && admin });
  const queue = useInfiniteQuery({ ...operatorTenantsQuery(REVIEW_QUEUE), enabled: live });
  const current = live && admin && !alerts.error ? alerts.data : undefined;
  const failed = live && admin && alerts.error !== null;
  const first = queue.data?.pages[0];
  const waiting = first ? first.items.length : 0;
  const more = first ? first.next_cursor !== null : false;
  const groups = demo
    ? demoNav(OPERATOR_NAV, "operator")
    : admin
      ? OPERATOR_NAV
      : OPERATOR_NAV.map((g) => ({ ...g, items: g.items.filter((i) => i.key !== "alerts") }));
  return (
    <Groups
      label="Operator"
      groups={groups}
      rest={rest}
      onNavigate={onNavigate}
      badges={{
        review:
          waiting > 0 ? (
            <NavCount
              tone="neutral"
              label={`${waiting === 1 && !more ? "1 project waits" : `${formatCount(waiting)}${more ? " or more" : ""} projects wait`} for review`}
            >
              {formatCount(waiting)}
              {more ? "+" : ""}
            </NavCount>
          ) : null,
        alerts: failed ? (
          <NavCount tone="critical" label="The alert source did not answer">
            <CircleAlert aria-hidden className="size-3.5" />
          </NavCount>
        ) : current && current.alerts.length > 0 ? (
          <NavCount
            tone={criticalCount(current.alerts) > 0 ? "critical" : "warning"}
            label={
              current.alerts.length === 1
                ? "1 alert firing"
                : `${current.alerts.length} alerts firing`
            }
          >
            {formatCount(current.alerts.length)}
          </NavCount>
        ) : null,
      }}
    />
  );
}

// the count at the end of a nav item: a plain number, quiet unless it is a
// problem (firing alerts). the words for a screen reader go in `label`
function NavCount({
  tone,
  label,
  children,
}: {
  tone: "neutral" | "warning" | "critical";
  label: string;
  children: ReactNode;
}) {
  return (
    <span
      data-tone={tone}
      className={cn(
        "inline-flex items-center text-caption font-normal tabular",
        tone === "neutral" ? "text-fg-3" : toneText(tone),
      )}
    >
      <span aria-hidden className="inline-flex items-center">
        {children}
      </span>
      <span className="sr-only">{label}</span>
    </span>
  );
}

// the alerts count: every firing alert, the number the Alerts page lists.
// critical while any of them pages, else warning
export function AlertsBadge({ alerts, className }: { alerts: FiringAlert[]; className?: string }) {
  if (alerts.length === 0) return null;
  const critical = criticalCount(alerts) > 0;
  return (
    <CountBadge
      tone={critical ? "critical" : "warning"}
      className={className}
      label={alerts.length === 1 ? "1 alert firing" : `${alerts.length} alerts firing`}
    >
      {formatCount(alerts.length)}
    </CountBadge>
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
        "group/item relative flex h-8 items-center gap-2.5 rounded-[8px] px-2 text-body transition-colors duration-(--vs-duration-press) ease-standard focus-ring active:bg-selected max-lg:h-(--vs-control-touch)",
        current ? "font-medium text-fg" : "text-fg-2 hover:bg-hover hover:text-fg",
      )}
    >
      {current ? (
        <motion.span
          layoutId="sidebar-current"
          transition={transition.layout}
          aria-hidden
          className="absolute inset-0 rounded-[8px] bg-hover"
        />
      ) : null}
      <Icon
        aria-hidden
        strokeWidth={1.8}
        className={cn("relative size-4 shrink-0", current ? "text-fg" : "text-fg-3")}
      />
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
    "flex h-[30px] items-center gap-2.5 rounded-[8px] px-2 text-small transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring [&_svg]:size-4 [&_svg]:text-fg-3 [&_svg]:stroke-[1.8] max-lg:h-(--vs-control-touch)",
    current ? "bg-hover text-fg" : "text-fg-2",
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
