import { useMutation, useQuery } from "@tanstack/react-query";
import { Mark } from "@veristar/brand/mark";
import { Menu as MenuIcon, Search, TriangleAlert, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, Outlet, useLocation, useNavigate } from "react-router";
import { api } from "@/api/endpoints";
import { stepUpQuery } from "@/api/operators";
import { meQuery, operatorAlertsQuery, tenantQuery } from "@/api/queries";
import { CommandPalette, isPaletteKey } from "@/components/command-palette";
import { FormMessage } from "@/components/error-panel";
import { PageMotion } from "@/components/motion";
import { areaOf, pageTitle } from "@/components/nav/nav-items";
import { AlertsBadge, Sidebar } from "@/components/nav/sidebar";
import { Kbd } from "@/components/panel";
import { Toaster, toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { MenuContainer } from "@/components/ui/menu";
import { TooltipProvider } from "@/components/ui/tooltip";
import { criticalCount } from "@/lib/alerts";
import { isNotFound } from "@/lib/errors";
import { queryClient } from "@/query-client";

// the signed-in frame: the sidebar on a wide screen, a top bar with the
// page title and a drawer on a phone, the toasts, the command palette
// (ctrl k) and, on operator pages while a paging alert fires, a note that
// links to Alerts. the note sits in the row of the Go to button (on a phone,
// a badge in the top bar), a row that is always there: the page never
// moves down when the note arrives. the page settles in when the route
// changes.
export function AppShell() {
  const me = useQuery(meQuery);
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [drawer, setDrawer] = useState(false);
  const [palette, setPalette] = useState(false);
  // a project that answers 404 names that in the top bar, not a page of it
  const area = areaOf(pathname);
  const asked = area.kind === "project" ? area.id : null;
  const tenant = useQuery({ ...tenantQuery(asked ?? ""), enabled: asked !== null });
  const gone = asked !== null && isNotFound(tenant.error);
  const signOut = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      toast.clear();
      navigate("/sign-in", { replace: true });
    },
  });

  // a navigation closes the drawer and starts the page at its top. focus
  // moves to the new page's content: a screen reader announces the page,
  // and the link that was clicked does not keep a focus ring
  useEffect(() => {
    setDrawer(false);
    window.scrollTo({ top: 0 });
    document.getElementById("main")?.focus({ preventScroll: true });
  }, [pathname]);

  // the tab names the page
  useEffect(() => {
    document.title = `${pageTitle(pathname)} · Veristar`;
  }, [pathname]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (!isPaletteKey(e)) return;
      e.preventDefault();
      setPalette((o) => !o);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (!me.data) return null;
  const operatorArea = me.data.user.is_operator && area.kind === "operator";
  const contextTitle = gone
    ? me.data.removed_tenants.some((r) => r.id === asked)
      ? "Project removed"
      : "Project not found"
    : area.kind === "project"
      ? (me.data.tenants.find((t) => t.id === area.id)?.name ?? "Veristar")
      : area.kind === "operator"
        ? "Operator area"
        : "Veristar";
  const side = (
    <Sidebar me={me.data} onSignOut={() => signOut.mutate()} onNavigate={() => setDrawer(false)} />
  );

  return (
    <TooltipProvider>
      <div className="min-h-dvh lg:grid lg:grid-cols-[var(--vs-sidebar)_minmax(0,1fr)]">
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-4 focus:z-(--vs-layer-toast) focus:rounded-md focus:bg-overlay focus:px-3 focus:py-2 focus:shadow-overlay"
        >
          Skip to content
        </a>
        <aside className="sticky top-0 hidden h-dvh border-r border-line bg-ground lg:block">
          {side}
        </aside>

        <header className="sticky top-0 z-(--vs-layer-header) flex h-(--vs-header) items-center gap-2 border-b border-line bg-ground px-2 lg:hidden">
          <Button
            variant="ghost"
            size="icon-lg"
            aria-label="Open navigation"
            aria-expanded={drawer}
            onClick={() => setDrawer(true)}
          >
            <MenuIcon aria-hidden />
          </Button>
          <Link to="/" className="rounded-md p-1 focus-ring" aria-label="Veristar, your projects">
            <Mark className="size-5" />
          </Link>
          {/* the context (the project, the operator area), never the page: the
              page's own title is the h1 right below */}
          <span className="min-w-0 flex-1 truncate text-title text-fg" data-testid="page-title">
            {contextTitle}
          </span>
          {operatorArea && pathname !== "/operator/alerts" ? <OperatorAlertButton /> : null}
          <Button
            variant="ghost"
            size="icon-lg"
            aria-label="Search pages and projects"
            onClick={() => setPalette(true)}
          >
            <Search aria-hidden />
          </Button>
        </header>
        <Drawer open={drawer} onClose={() => setDrawer(false)}>
          {side}
        </Drawer>

        <div className="flex min-w-0 flex-col">
          <div className="hidden min-h-12 items-start justify-end gap-4 px-6 pt-4 lg:flex xl:px-10">
            {operatorArea && pathname !== "/operator/alerts" ? <OperatorAlertLine /> : null}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setPalette(true)}
              className="text-fg-2"
              aria-keyshortcuts="Control+K Meta+K"
            >
              <Search aria-hidden />
              Go to
              <Kbd>Ctrl K</Kbd>
            </Button>
          </div>
          {signOut.error ? (
            <div className="px-4 pt-4 sm:px-6">
              <FormMessage error={signOut.error} />
            </div>
          ) : null}
          <main
            id="main"
            tabIndex={-1}
            className="mx-auto w-full max-w-content flex-1 px-4 pt-6 pb-16 outline-none sm:px-6 lg:pt-2 xl:px-10"
          >
            <PageMotion key={pathname}>
              <Outlet />
            </PageMotion>
          </main>
        </div>
      </div>
      <CommandPalette me={me.data} open={palette} onOpenChange={setPalette} />
      <Toaster />
    </TooltipProvider>
  );
}

// the paging (critical) alerts while any fires, read only once the session
// holds a step-up, as the operator area does. not on Alerts itself
function useCriticalAlerts() {
  const stepUp = useQuery(stepUpQuery);
  const live = stepUp.data?.stepped_up === true;
  const alerts = useQuery({ ...operatorAlertsQuery, enabled: live });
  const current = live && !alerts.error ? alerts.data : undefined;
  return current ? current.alerts.filter((a) => criticalCount([a]) === 1) : [];
}

function OperatorAlertLine() {
  const critical = useCriticalAlerts().length;
  if (critical === 0) return null;
  return (
    <div
      role="alert"
      className="mr-auto flex h-(--vs-control-sm) min-w-0 animate-fade-in items-center gap-2 rounded-md border border-critical-line bg-critical-soft px-3 text-small text-fg"
    >
      <TriangleAlert aria-hidden className="size-4 shrink-0 text-critical" />
      <span className="min-w-0 truncate">
        {critical === 1 ? "1 critical alert is firing." : `${critical} critical alerts are firing.`}
      </span>
      <Link
        to="/operator/alerts"
        className="shrink-0 rounded-sm font-medium text-fg underline decoration-line-strong underline-offset-4 hover:decoration-fg focus-ring"
      >
        Open alerts
      </Link>
    </div>
  );
}

// the same on a phone: a button with the count in the top bar
function OperatorAlertButton() {
  const critical = useCriticalAlerts();
  if (critical.length === 0) return null;
  return (
    <Button asChild variant="ghost" size="lg" className="gap-1 px-2 text-critical">
      <Link to="/operator/alerts">
        <TriangleAlert aria-hidden />
        <AlertsBadge alerts={critical} />
        <span className="sr-only">Open alerts</span>
      </Link>
    </Button>
  );
}

// the phone navigation: the same sidebar in a native modal dialog that
// slides in from the left edge
function Drawer({
  open,
  onClose,
  children,
}: {
  open: boolean;
  onClose: () => void;
  children: React.ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [box, setBox] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: the click only closes on the scrim; Escape closes the native dialog
    <dialog
      ref={ref}
      aria-label="Navigation"
      onClose={onClose}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className="drawer-motion m-0 h-dvh max-h-dvh w-(--vs-sidebar) max-w-[85vw] border-r border-line bg-ground p-0 text-fg lg:hidden"
    >
      <div ref={setBox} className="relative h-full">
        <Button
          variant="ghost"
          size="icon-lg"
          aria-label="Close navigation"
          className="absolute top-2 right-2 z-(--vs-layer-raised)"
          onClick={onClose}
        >
          <X aria-hidden />
        </Button>
        {/* the switcher and the account menu open inside the drawer */}
        <MenuContainer.Provider value={box}>{children}</MenuContainer.Provider>
      </div>
    </dialog>
  );
}
