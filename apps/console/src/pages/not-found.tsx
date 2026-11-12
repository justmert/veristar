import { isRouteErrorResponse, Link, useRouteError } from "react-router";
import { ErrorPanel } from "@/components/error-panel";

export function NotFoundPage() {
  return (
    <section className="max-w-prose py-8">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <p className="mt-2 text-muted-foreground">
        This address does not exist in the console, or the project behind it is not yours.
      </p>
      <Link to="/" className="mt-4 inline-block text-primary underline underline-offset-4">
        Go to your projects
      </Link>
    </section>
  );
}

export function RouteErrorPage() {
  const error = useRouteError();
  if (isRouteErrorResponse(error) && error.status === 404) {
    return (
      <div className="mx-auto max-w-6xl px-4 py-10 sm:px-6">
        <NotFoundPage />
      </div>
    );
  }
  return (
    <div className="mx-auto max-w-2xl px-4 py-10 sm:px-6">
      <ErrorPanel
        error={error}
        title="The console hit an error"
        onRetry={() => window.location.reload()}
      />
    </div>
  );
}
