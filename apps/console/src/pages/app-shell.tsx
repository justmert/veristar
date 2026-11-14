import { useMutation, useQuery } from "@tanstack/react-query";
import { Mark } from "@veristar/brand/mark";
import { Menu as MenuIcon, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link, Outlet, useLocation, useNavigate } from "react-router";
import { api } from "@/api/endpoints";
import { meQuery } from "@/api/queries";
import { FormMessage } from "@/components/error-panel";
import { PageMotion } from "@/components/motion";
import { Sidebar } from "@/components/nav/sidebar";
import { Button } from "@/components/ui/button";
import { TooltipProvider } from "@/components/ui/tooltip";
import { queryClient } from "@/query-client";

// the signed-in frame: the sidebar on a wide screen, a top bar with a drawer
// on a phone, and the page, which settles in when the route changes
export function AppShell() {
  const me = useQuery(meQuery);
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [drawer, setDrawer] = useState(false);
  const signOut = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/sign-in", { replace: true });
    },
  });

  // a navigation closes the drawer and starts the page at its top
  // focus moves to the new page's content: a screen reader announces the
  // page, and the link that was clicked does not keep a focus ring
  useEffect(() => {
    setDrawer(false);
    window.scrollTo({ top: 0 });
    document.getElementById("main")?.focus({ preventScroll: true });
  }, [pathname]);

  if (!me.data) return null;
  const side = (
    <Sidebar me={me.data} onSignOut={() => signOut.mutate()} onNavigate={() => setDrawer(false)} />
  );

  return (
    <TooltipProvider>
      <div className="min-h-dvh lg:grid lg:grid-cols-[var(--vs-sidebar)_minmax(0,1fr)]">
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-4 focus:z-50 focus:rounded-md focus:bg-overlay focus:px-3 focus:py-2 focus:shadow-overlay"
        >
          Skip to content
        </a>
        <aside className="sticky top-0 hidden h-dvh border-r border-line bg-ground lg:block">
          {side}
        </aside>

        <header className="sticky top-0 z-30 flex h-(--vs-header) items-center gap-2 border-b border-line bg-ground px-2 lg:hidden">
          <Button
            variant="ghost"
            size="icon-lg"
            aria-label="Open navigation"
            aria-expanded={drawer}
            onClick={() => setDrawer(true)}
          >
            <MenuIcon aria-hidden />
          </Button>
          <Link
            to="/"
            className="flex items-center gap-2 rounded-md px-1 py-1 focus-ring"
            aria-label="Veristar, your projects"
          >
            <Mark className="size-5" />
            <span className="text-title tracking-tight">Veristar</span>
          </Link>
        </header>
        <Drawer open={drawer} onClose={() => setDrawer(false)}>
          {side}
        </Drawer>

        <div className="flex min-w-0 flex-col">
          {signOut.error ? (
            <div className="px-4 pt-4 sm:px-8">
              <FormMessage error={signOut.error} />
            </div>
          ) : null}
          <main
            id="main"
            tabIndex={-1}
            className="mx-auto w-full max-w-content flex-1 px-4 pt-6 pb-16 outline-none sm:px-8 sm:pt-8 xl:px-10"
          >
            <PageMotion key={pathname}>
              <Outlet />
            </PageMotion>
          </main>
        </div>
      </div>
    </TooltipProvider>
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
      <div className="relative h-full">
        <Button
          variant="ghost"
          size="icon-lg"
          aria-label="Close navigation"
          className="absolute top-2 right-2"
          onClick={onClose}
        >
          <X aria-hidden />
        </Button>
        {children}
      </div>
    </dialog>
  );
}
