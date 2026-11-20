import { useMutation } from "@tanstack/react-query";
import { LockKeyhole, LogOut, RotateCcw, TriangleAlert } from "lucide-react";
import { isRouteErrorResponse, Link, useNavigate, useRouteError } from "react-router";
import { api } from "@/api/endpoints";
import { CopyButton } from "@/components/copy-button";
import { FormMessage } from "@/components/error-panel";
import { GoToButton } from "@/components/nav/go-to";
import { GoToButton } from "@/components/nav/go-to";
import { PublicFrame } from "@/components/public-frame";
import { MessagePage } from "@/components/states";
import { Button } from "@/components/ui/button";
import { errorDetails } from "@/lib/errors";
import { queryClient } from "@/query-client";

export function ProjectsButton() {
  return (
    <Button asChild className="max-sm:h-(--vs-control-touch)">
      <Link to="/">Go to your projects</Link>
    </Button>
  );
}

export function NotFoundPage({ project = false }: { project?: boolean }) {
  return (
    <MessagePage
      code="404"
      title={project ? "Project not found" : "Page not found"}
      actions={
        <>
          <ProjectsButton />
          <GoToButton />
        </>
      }
    >
      <p>
        {project
          ? "This project does not exist, or you are not a member of it."
          : "This address is not in the console, or its project is not yours."}
      </p>
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
      code="403"
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
  // one way on: reload. the details go to the operators when it stays
  return (
    <PublicFrame>
      <MessagePage
        code="Error"
        tone="critical"
        title="The console did not load"
        actions={
          <>
            <Button
              className="max-sm:h-(--vs-control-touch)"
              onClick={() => window.location.reload()}
            >
              <RotateCcw aria-hidden />
              Reload
            </Button>
            <CopyButton
              text={errorDetails(error)}
              label="Copy details"
              visibleLabel="Copy details"
              variant="ghost"
              size="default"
            />
          </>
        }
      >
        <p role="alert">
          Reload the page. If the error stays, send the copied details to the operators.
        </p>
      </MessagePage>
    </PublicFrame>
  );
}
