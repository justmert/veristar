import { useMutation } from "@tanstack/react-query";
import { LockKeyhole, LogOut, RotateCcw, SearchX, TriangleAlert } from "lucide-react";
import { isRouteErrorResponse, Link, useNavigate, useRouteError } from "react-router";
import { api } from "@/api/endpoints";
import { CopyButton } from "@/components/copy-button";
import { FormMessage } from "@/components/error-panel";
import { PublicFrame } from "@/components/public-frame";
import { MessagePage } from "@/components/states";
import { Button } from "@/components/ui/button";
import { errorDetails, errorMessage } from "@/lib/errors";
import { queryClient } from "@/query-client";

export function ProjectsButton() {
  return (
    <Button asChild className="max-sm:h-(--vs-control-touch)">
      <Link to="/">Go to your projects</Link>
    </Button>
  );
}

export function NotFoundPage() {
  return (
    <MessagePage icon={<SearchX aria-hidden />} title="Page not found" actions={<ProjectsButton />}>
      <p>This address does not exist in the console, or the project behind it is not yours.</p>
    </MessagePage>
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
    <MessagePage
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
        This area is for operators of the service. Your account is not on the operator list. If it
        was added recently, sign out and sign in again.
      </p>
      <FormMessage error={signOut.error} className="mt-2" />
    </MessagePage>
  );
}

export function RouteErrorPage() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) {
    return (
      <PublicFrame>
        <NotFoundPage />
      </PublicFrame>
    );
  }
  return (
    <PublicFrame>
      <MessagePage
        icon={<TriangleAlert aria-hidden />}
        tone="critical"
        title="The console hit an error"
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
            <CopyButton
              text={errorDetails(error)}
              label="Copy details"
              variant="ghost"
              size="default"
            />
          </>
        }
      >
        <p role="alert">{errorMessage(error)}</p>
        <p>Reload the page. If the error stays, send the copied details to the operators.</p>
      </MessagePage>
    </PublicFrame>
  );
}
