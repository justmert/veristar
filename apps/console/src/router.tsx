import { createBrowserRouter, type LoaderFunctionArgs, redirect } from "react-router";
import { ApiError } from "@/api/client";
import { meQuery } from "@/api/queries";
import { AppShell } from "@/pages/app-shell";
import { EnrollPage } from "@/pages/enroll";
import { MembersPage } from "@/pages/members";
import { NotFoundPage, RouteErrorPage } from "@/pages/not-found";
import { AuditPage } from "@/pages/operator/audit";
import { OperatorLayout } from "@/pages/operator/layout";
import { ReviewQueuePage } from "@/pages/operator/review";
import { SettingsPage } from "@/pages/operator/settings";
import { TenantDetailPage } from "@/pages/operator/tenant-detail";
import { TenantsPage } from "@/pages/operator/tenants";
import { OverviewPage } from "@/pages/overview";
import { ProjectLayout } from "@/pages/project-layout";
import { ProjectsPage } from "@/pages/projects";
import { SignInPage } from "@/pages/sign-in";
import { SitesPage } from "@/pages/sites";
import { SnippetPage } from "@/pages/snippet";
import { SsoErrorPage } from "@/pages/sso-error";
import { TermsPage } from "@/pages/terms";
import { VerifyPage } from "@/pages/verify";
import { queryClient } from "@/query-client";

// every signed-in page needs /api/me first; without a session the browser
// goes to sign-in and comes back to the page it asked for
async function requireSession({ request }: LoaderFunctionArgs) {
  try {
    return await queryClient.ensureQueryData(meQuery);
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) {
      const url = new URL(request.url);
      throw redirect(`/sign-in?return_to=${encodeURIComponent(url.pathname + url.search)}`);
    }
    throw err;
  }
}

export const router = createBrowserRouter([
  { path: "/sign-in", element: <SignInPage />, errorElement: <RouteErrorPage /> },
  { path: "/terms", element: <TermsPage />, errorElement: <RouteErrorPage /> },
  { path: "/sso-error", element: <SsoErrorPage />, errorElement: <RouteErrorPage /> },
  {
    path: "/",
    id: "session",
    loader: requireSession,
    element: <AppShell />,
    // shown while the session check of the first page load runs
    hydrateFallbackElement: (
      <p role="status" className="p-8 text-sm text-muted-foreground">
        Loading the console
      </p>
    ),
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, element: <ProjectsPage /> },
      { path: "projects/new", element: <EnrollPage /> },
      {
        path: "projects/:tenantId",
        element: <ProjectLayout />,
        children: [
          { index: true, element: <OverviewPage /> },
          { path: "sites", element: <SitesPage /> },
          { path: "sites/:siteId/snippet", element: <SnippetPage /> },
          { path: "sites/:siteId/verify", element: <VerifyPage /> },
          { path: "members", element: <MembersPage /> },
        ],
      },
      {
        path: "operator",
        element: <OperatorLayout />,
        children: [
          { index: true, element: <ReviewQueuePage /> },
          { path: "tenants", element: <TenantsPage /> },
          { path: "tenants/:tenantId", element: <TenantDetailPage /> },
          { path: "audit", element: <AuditPage /> },
          { path: "settings", element: <SettingsPage /> },
        ],
      },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
