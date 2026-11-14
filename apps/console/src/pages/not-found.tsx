import { useMutation } from "@tanstack/react-query";
import { cn } from "cn";
import { LockKeyhole, LogOut, RotateCcw, SearchX, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";
import { isRouteErrorResponse, Link, useNavigate, useRouteError } from "react-router";
import { ApiError } from "@/api/client";
import { api } from "@/api/endpoints";
import { FormMessage } from "@/components/error-panel";
import { PublicFrame } from "@/components/public-frame";
import { Button } from "@/components/ui/button";
import { errorText } from "@/lib/errors";
import { queryClient } from "@/query-client";

// a whole page that stands in for the one asked for (not found, removed,
// operators only, a crash): what happened, why, and the way on
export function StopPage({
  icon,
  tone = "neutral",
  title,
  children,
  reference,
  actions,
  className,
}: {
  icon: ReactNode;
  tone?: "neutral" | "critical";
  title: string;
  children?: ReactNode;
  reference?: string | null;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <section
      className={cn(
        "flex min-h-80 max-w-prose animate-rise-in flex-col justify-center gap-4 rounded-lg border border-line px-6 py-10 sm:px-10 sm:py-12",
        className,
      )}
    >
      <span
        className={cn(
          "flex size-10 items-center justify-center rounded-lg border border-line-strong [&_svg]:size-5",
          tone === "critical" ? "text-critical" : "text-fg-2",
        )}
      >
        {icon}
      </span>
      <h1 className="text-page tracking-tight text-fg">{title}</h1>
      {children ? (
        <div className="flex max-w-prose flex-col gap-1 text-body text-fg-2">{children}</div>
      ) : null}
      {reference ? (
        <p className="font-mono text-caption font-normal text-fg-3">{reference}</p>
      ) : null}
      {actions ? <div className="flex flex-wrap gap-2.5 pt-1">{actions}</div> : null}
    </section>
  );
}

export function ProjectsButton() {
  return (
    <Button asChild className="max-sm:h-(--vs-control-touch)">
      <Link to="/">Go to your projects</Link>
    </Button>
  );
}

export function NotFoundPage() {
  return (
    <StopPage icon={<SearchX aria-hidden />} title="Page not found" actions={<ProjectsButton />}>
      <p>This address does not exist in the console, or the project behind it is not yours.</p>
    </StopPage>
  );
}

// /operator for a signed-in person who is not an operator; signing out and in
// again picks up a recent addition to the operator list
export function OperatorsOnlyPage() {
  const navigate = useNavigate();
  const signOut = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/sign-in", { replace: true });
    },
  });
  return (
    <StopPage
      icon={<LockKeyhole aria-hidden />}
      title="Operators only"
      actions={
        <>
          <ProjectsButton />
          <Button
            variant="outline"
            className="max-sm:h-(--vs-control-touch)"
            pending={signOut.isPending}
            onClick={() => signOut.mutate()}
          >
            <LogOut aria-hidden />
            Sign out
          </Button>
        </>
      }
    >
      <p>
        This area is for operators of the service. Your GitHub account is not on the operator list.
        If it was added recently, sign out and sign in again.
      </p>
      <FormMessage error={signOut.error} className="mt-2" />
    </StopPage>
  );
}

export function RouteErrorPage() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) {
    return (
      <PublicFrame footer={false}>
        <NotFoundPage />
      </PublicFrame>
    );
  }
  const text = errorText(error);
  // some error texts already carry the request id; it is shown once
  const ref =
    error instanceof ApiError && error.requestId && !text.includes(error.requestId)
      ? `Reference ${error.requestId}`
      : null;
  return (
    <PublicFrame footer={false}>
      <StopPage
        icon={<TriangleAlert aria-hidden />}
        tone="critical"
        title="The console hit an error"
        reference={ref}
        className="w-full"
        actions={
          <>
            <Button
              className="max-sm:h-(--vs-control-touch)"
              onClick={() => window.location.reload()}
            >
              <RotateCcw aria-hidden />
              Reload
            </Button>
            <Button asChild variant="outline" className="max-sm:h-(--vs-control-touch)">
              <Link to="/">Go to your projects</Link>
            </Button>
          </>
        }
      >
        <p role="alert">{text}</p>
        <p>Reload the page. If it happens again, report it with the text above.</p>
      </StopPage>
    </PublicFrame>
  );
}
