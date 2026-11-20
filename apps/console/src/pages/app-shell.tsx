import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
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
import { PaletteContext } from "@/components/nav/go-to";
import { areaOf, pageTitle } from "@/components/nav/nav-items";
import { useOperatorAdmin } from "@/components/nav/operator-role";
import { AlertsBadge, Sidebar } from "@/components/nav/sidebar";
import { Toaster, toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { MenuContainer } from "@/components/ui/menu";
import { TooltipProvider } from "@/components/ui/tooltip";
import { criticalCount } from "@/lib/alerts";
import { isNotFound } from "@/lib/errors";
import { queryClient } from "@/query-client";

// the signed-in frame: the sidebar on a wide screen, a top bar with the
// context and a drawer on a phone, the toasts and the command palette
// (ctrl k, or Go to in the sidebar). firing alerts are signalled by the Alerts badge in the sidebar
// (on a phone, a badge in the top bar), for operator admins only; nothing
// is drawn above the page title. the page settles in when the route changes.
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

  // the tab names the page; the favicon already says veristar, so the title
  // joins nothing to it
  useEffect(() => {
    document.title = pageTitle(pathname);
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
  const openPalette = () => {
    setDrawer(false);
    setPalette(true);
  };
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
    <PaletteContext.Provider value={openPalette}>
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
            {signOut.error ? (
              <div className="px-4 pt-4 sm:px-6">
                <FormMessage error={signOut.error} />
              </div>
            ) : null}
            <main
              id="main"
              tabIndex={-1}
              className="mx-auto flex w-full max-w-[1200px] flex-1 flex-col px-4 pt-6 pb-16 outline-none sm:px-6 lg:px-10 lg:pt-7"
            >
              {/* a column that fills the window, so a message page (404,
                  operators only) centres in it as on the canvas */}
              <PageMotion key={pathname} className="flex flex-1 flex-col">
                <Outlet />
              </PageMotion>
            </main>
          </div>
        </div>
        <CommandPalette me={me.data} open={palette} onOpenChange={setPalette} />
        <Toaster />
      </TooltipProvider>
    </PaletteContext.Provider>
  );
}

// on a phone, the firing alerts as a badge in the top bar, for operator
// admins once the session holds a step-up. not on Alerts itself
function OperatorAlertButton() {
  const stepUp = useQuery(stepUpQuery);
  const admin = useOperatorAdmin() === true;
  const live = stepUp.data?.stepped_up === true && admin;
  const alerts = useQuery({ ...operatorAlertsQuery, enabled: live });
  const firing = live && !alerts.error ? (alerts.data?.alerts ?? []) : [];
  if (firing.length === 0) return null;
  return (
    <Button
      asChild
      variant="ghost"
      size="lg"
      className={cn("gap-1 px-2", criticalCount(firing) > 0 ? "text-critical" : "text-warning")}
    >
      <Link to="/operator/alerts">
        <TriangleAlert aria-hidden />
        <AlertsBadge alerts={firing} />
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
