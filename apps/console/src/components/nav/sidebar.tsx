import { Mark } from "@veristar/brand/mark";
import { transition } from "@veristar/brand/motion";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  Activity,
  BookOpen,
  Check,
  ChevronsUpDown,
  Ellipsis,
  FolderOpen,
  LogOut,
  Plus,
  ScrollText,
} from "lucide-react";
import { LayoutGroup, motion } from "motion/react";
import { type ReactNode, useId } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { operatorAlertsQuery, tenantQuery } from "@/api/queries";
import type { Me } from "@/api/schemas";
import { StatusDot, statusTone } from "@/components/status";
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
} from "@/components/ui/menu";
import { criticalCount } from "@/lib/alerts";
import { siblingUrl } from "@/lib/hosts";
import { type NavItem, OPERATOR_NAV, projectNav } from "./nav-items";

const STATUS_WORD: Record<string, string> = {
  active: "Active",
  pending: "Pending review",
  suspended: "Suspended",
  rejected: "Not approved",
  removed: "Deleted",
};

// where the browser is, read from the path: a project, the operator area or
// the project list
export function useArea() {
  const { pathname } = useLocation();
  const p = pathname.match(/^\/projects\/([^/]+)(?:\/(.*))?$/);
  if (p?.[1] && p[1] !== "new") return { kind: "project" as const, id: p[1], rest: p[2] ?? "" };
  const o = pathname.match(/^\/operator(?:\/(.*))?$/);
  if (o) return { kind: "operator" as const, rest: o[1] ?? "" };
  return { kind: "home" as const, rest: pathname.replace(/^\//, "") };
}

export function Sidebar({
  me,
  onSignOut,
  onNavigate,
}: {
  me: Me;
  onSignOut: () => void;
  onNavigate?: () => void;
}) {
  const area = useArea();
  const docs = siblingUrl(window.location.hostname, "docs");
  const status = siblingUrl(window.location.hostname, "status");
  // the desktop sidebar and the phone drawer are two instances; each needs
  // its own group, or their current-item markers would crossfade
  const group = useId();
  return (
    <LayoutGroup id={group}>
      <div className="flex h-full flex-col gap-3.5 px-2.5 py-3.5">
        <Link
          to="/"
          onClick={onNavigate}
          aria-label="Veristar, your projects"
          className="flex items-center gap-2 self-start rounded-md px-2 py-1 text-fg focus-ring"
        >
          <Mark className="size-5.5" />
          <span className="text-title tracking-tight">Veristar</span>
        </Link>

        {area.kind === "project" ? (
          <ProjectSection me={me} id={area.id} rest={area.rest} onNavigate={onNavigate} />
        ) : (
          <nav aria-label="Projects" className="flex flex-col gap-px">
            <Item
              item={{
                key: "projects",
                label: "Your projects",
                to: "/",
                icon: FolderOpen,
                match: () => true,
              }}
              current={area.kind === "home" && area.rest !== "projects/new"}
              onNavigate={onNavigate}
            />
            <Item
              item={{
                key: "new",
                label: "Register a project",
                to: "/projects/new",
                icon: Plus,
                match: () => true,
              }}
              current={area.kind === "home" && area.rest === "projects/new"}
              onNavigate={onNavigate}
            />
          </nav>
        )}

        {me.user.is_operator ? (
          <OperatorSection
            inOperator={area.kind === "operator"}
            rest={area.kind === "operator" ? area.rest : null}
            onNavigate={onNavigate}
          />
        ) : null}

        <div className="mt-auto flex flex-col gap-px border-t border-line pt-2.5">
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
          <Menu>
            <MenuTrigger asChild>
              <button
                type="button"
                className="mt-1 flex items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-fg transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring aria-expanded:bg-selected"
              >
                <span className="flex size-6.5 shrink-0 items-center justify-center rounded-full bg-selected text-axis font-semibold uppercase">
                  {me.user.github_login.slice(0, 2)}
                </span>
                <span className="min-w-0 flex-1 truncate text-small">{me.user.github_login}</span>
                <Ellipsis aria-hidden className="size-3.5 text-fg-3" />
                <span className="sr-only">, account menu</span>
              </button>
            </MenuTrigger>
            <MenuContent
              side="top"
              align="start"
              className="w-(--radix-dropdown-menu-trigger-width)"
            >
              <MenuLabel>Signed in as {me.user.github_login}</MenuLabel>
              <MenuSeparator />
              <MenuItem onSelect={onSignOut}>
                <LogOut aria-hidden />
                Sign out
              </MenuItem>
            </MenuContent>
          </Menu>
        </div>
      </div>
    </LayoutGroup>
  );
}

function ProjectSection({
  me,
  id,
  rest,
  onNavigate,
}: {
  me: Me;
  id: string;
  rest: string;
  onNavigate?: () => void;
}) {
  const tenant = useQuery(tenantQuery(id));
  const navigate = useNavigate();
  const listed = me.tenants.find((t) => t.id === id);
  const name = tenant.data?.name ?? listed?.name ?? "Project";
  const st = tenant.data?.status ?? listed?.status;
  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <button
            type="button"
            className="grid grid-cols-[28px_minmax(0,1fr)_14px] items-center gap-2.5 rounded-md border border-line bg-raised p-2 text-left text-fg transition-colors duration-(--vs-duration-press) hover:border-line-strong hover:bg-hover focus-ring aria-expanded:border-line-strong"
          >
            <span className="flex size-7 items-center justify-center rounded-md bg-selected text-caption font-semibold uppercase">
              {name.slice(0, 1)}
            </span>
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-body font-semibold">{name}</span>
              {st ? (
                <span className="flex items-center gap-1.5 text-caption font-normal text-fg-2">
                  <StatusDot tone={statusTone(st)} />
                  {STATUS_WORD[st] ?? st}
                </span>
              ) : null}
            </span>
            <ChevronsUpDown aria-hidden className="size-3.5 text-fg-3" />
            <span className="sr-only">, switch project</span>
          </button>
        </MenuTrigger>
        <MenuContent className="w-(--radix-dropdown-menu-trigger-width) min-w-60">
          <MenuLabel>Your projects</MenuLabel>
          {me.tenants.map((t) => (
            <MenuItem
              key={t.id}
              onSelect={() => {
                navigate(`/projects/${t.id}`);
                onNavigate?.();
              }}
            >
              <StatusDot tone={statusTone(t.status)} />
              <span className="min-w-0 flex-1 truncate">{t.name}</span>
              {t.id === id ? <Check aria-hidden /> : null}
            </MenuItem>
          ))}
          <MenuSeparator />
          <MenuItem
            onSelect={() => {
              navigate("/");
              onNavigate?.();
            }}
          >
            <FolderOpen aria-hidden />
            All projects
          </MenuItem>
          <MenuItem
            onSelect={() => {
              navigate("/projects/new");
              onNavigate?.();
            }}
          >
            <Plus aria-hidden />
            Register a project
          </MenuItem>
        </MenuContent>
      </Menu>
      <nav aria-label="Project" className="flex flex-col gap-px">
        {projectNav(id).map((n) => (
          <Item key={n.key} item={n} current={n.match(rest)} onNavigate={onNavigate} />
        ))}
      </nav>
    </>
  );
}

function OperatorSection({
  inOperator,
  rest,
  onNavigate,
}: {
  inOperator: boolean;
  rest: string | null;
  onNavigate?: () => void;
}) {
  // alerts are polled only inside the operator area (contract 5.13)
  const alerts = useQuery({ ...operatorAlertsQuery, enabled: inOperator });
  const current = inOperator && !alerts.error ? alerts.data : undefined;
  const critical = current ? criticalCount(current.alerts) : 0;
  const count = current?.alerts.length ?? 0;
  const failed = inOperator && alerts.error !== null;
  return (
    <nav aria-label="Operator" className="flex flex-col gap-px">
      <span className="px-2.5 py-1.5 text-caption text-fg-3">OPERATOR</span>
      {OPERATOR_NAV.map((n) => (
        <Item
          key={n.key}
          item={n}
          current={rest !== null && n.match(rest)}
          onNavigate={onNavigate}
          badge={
            n.key === "attention" && (failed || count > 0) ? (
              <span
                className={cn(
                  "flex items-center gap-1 font-mono text-axis",
                  failed || critical ? "text-critical" : "text-warning",
                )}
              >
                <StatusDot tone={failed || critical ? "critical" : "warning"} live={critical > 0} />
                {failed ? "!" : count}
                <span className="sr-only">
                  {failed
                    ? ", alert source unreachable"
                    : count === 1
                      ? " alert firing"
                      : " alerts firing"}
                </span>
              </span>
            ) : null
          }
        />
      ))}
    </nav>
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
        "relative flex h-8 items-center gap-2.5 rounded-md px-2.5 text-body transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:h-(--vs-control-touch)",
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
  children,
}: {
  href?: string;
  to?: string;
  icon: ReactNode;
  children: ReactNode;
}) {
  const cls =
    "flex h-7.5 items-center gap-2.5 rounded-md px-2.5 text-small text-fg-2 transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring [&_svg]:size-3.75 max-lg:h-(--vs-control-touch)";
  return to ? (
    <Link to={to} className={cls}>
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
