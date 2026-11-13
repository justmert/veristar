import { useMutation, useQuery } from "@tanstack/react-query";
import { LogOut } from "lucide-react";
import { Link, Outlet, useNavigate } from "react-router";
import { api } from "@/api/endpoints";
import { meQuery } from "@/api/queries";
import { FormMessage } from "@/components/error-panel";
import { Mark } from "@/components/mark";
import { Button } from "@/components/ui/button";
import { queryClient } from "@/query-client";

export function AppShell() {
  const me = useQuery(meQuery);
  const navigate = useNavigate();
  const signOut = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/sign-in", { replace: true });
    },
  });

  return (
    <div className="flex min-h-dvh flex-col">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-50 focus:rounded-md focus:bg-card focus:px-3 focus:py-2"
      >
        Skip to content
      </a>
      <header className="border-b bg-card/70 backdrop-blur supports-[backdrop-filter]:bg-card/60">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4 sm:px-6">
          <Link to="/" className="flex items-center gap-2 rounded-md font-semibold tracking-tight">
            <Mark className="size-6 text-primary" />
            <span>Veristar</span>
          </Link>
          <div className="ml-auto flex items-center gap-2 text-sm">
            {me.data?.user.is_operator ? (
              <Button asChild variant="ghost" size="sm">
                <Link to="/operator">Operator</Link>
              </Button>
            ) : null}
            {me.data ? (
              <span className="hidden text-muted-foreground sm:inline">
                Signed in as{" "}
                <span className="font-medium text-foreground">{me.data.user.github_login}</span>
              </span>
            ) : null}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => signOut.mutate()}
              disabled={signOut.isPending}
            >
              <LogOut aria-hidden />
              Sign out
            </Button>
          </div>
        </div>
        {signOut.error ? (
          <div className="mx-auto max-w-6xl px-4 pb-2 sm:px-6">
            <FormMessage error={signOut.error} />
          </div>
        ) : null}
      </header>
      <main id="main" className="mx-auto w-full max-w-6xl flex-1 px-4 py-8 sm:px-6">
        <Outlet />
      </main>
      <footer className="border-t">
        <div className="mx-auto flex max-w-6xl flex-wrap gap-x-4 gap-y-1 px-4 py-4 text-xs text-muted-foreground sm:px-6">
          <Link to="/terms" className="hover:text-foreground">
            Terms
          </Link>
          <span>No fees, no ads, no cookies on your visitors.</span>
        </div>
      </footer>
    </div>
  );
}
