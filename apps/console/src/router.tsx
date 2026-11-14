import { Mark } from "@veristar/brand/mark";
import { createBrowserRouter, type LoaderFunctionArgs, redirect } from "react-router";
import { ApiError } from "@/api/client";
import { meQuery } from "@/api/queries";
import { AccountPage } from "@/pages/account";
import { ApiSetupPage } from "@/pages/api-setup";
import { ApiTrafficPage } from "@/pages/api-traffic";
import { AppShell } from "@/pages/app-shell";
import { EnrollPage } from "@/pages/enroll";
import { McpPage } from "@/pages/mcp";
import { McpConsentPage } from "@/pages/mcp-consent";
import { MembersPage } from "@/pages/members";
import { NotFoundPage, RouteErrorPage } from "@/pages/not-found";
import { AlertsPage } from "@/pages/operator/alerts";
import { ApiKeysPage } from "@/pages/operator/api-keys";
import { AuditPage } from "@/pages/operator/audit";
import { ComparePage } from "@/pages/operator/compare";
import { GlobalApiPage } from "@/pages/operator/global-api";
import { GlobalWebPage } from "@/pages/operator/global-web";
import { OperatorLayout } from "@/pages/operator/layout";
import { OperatorMcpPage } from "@/pages/operator/mcp";
import { ReviewQueuePage } from "@/pages/operator/review";
import { SettingsPage } from "@/pages/operator/settings";
import { TenantDetailPage } from "@/pages/operator/tenant-detail";
import { TenantsPage } from "@/pages/operator/tenants";
import { OverviewPage } from "@/pages/overview";
import { PrivacyPage } from "@/pages/privacy";
import { ProjectLayout } from "@/pages/project-layout";
import { ProjectsPage } from "@/pages/projects";
import { SignInPage } from "@/pages/sign-in";
import { SitesPage } from "@/pages/sites";
import { SnippetPage } from "@/pages/snippet";
import { SsoErrorPage } from "@/pages/sso-error";
import { TermsPage } from "@/pages/terms";
import { VerifyPage } from "@/pages/verify";
import { queryClient } from "@/query-client";

// the first paint while the session check runs: the mark breathing in the
// middle of the ground, so a slow network shows the console is on its way
function ConsoleLoading() {
  return (
    <div
      role="status"
      className="flex min-h-dvh flex-col items-center justify-center gap-4 bg-ground text-fg"
    >
      <Mark className="size-10 animate-skeleton" />
      <span className="animate-fade-in text-small text-fg-2">Loading the console</span>
    </div>
  );
}

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
  // the mcp oauth consent screen; it signs the person in itself, so the
  // sealed request survives the github round trip (contract 12.3)
  { path: "/ai/consent", element: <McpConsentPage />, errorElement: <RouteErrorPage /> },
  {
    path: "/",
    id: "session",
    loader: requireSession,
    element: <AppShell />,
    // shown while the session check of the first page load runs
    hydrateFallbackElement: <ConsoleLoading />,
    errorElement: <RouteErrorPage />,
    children: [
      { index: true, element: <ProjectsPage /> },
      { path: "projects/new", element: <EnrollPage /> },
      { path: "account", element: <AccountPage /> },
      {
        path: "projects/:tenantId",
        element: <ProjectLayout />,
        children: [
          { index: true, element: <OverviewPage /> },
          { path: "sites", element: <SitesPage /> },
          { path: "sites/:siteId/snippet", element: <SnippetPage /> },
          { path: "sites/:siteId/verify", element: <VerifyPage /> },
          { path: "members", element: <MembersPage /> },
          { path: "privacy", element: <PrivacyPage /> },
          { path: "api", element: <ApiSetupPage /> },
          { path: "api/traffic", element: <ApiTrafficPage /> },
          { path: "ai", element: <McpPage /> },
        ],
      },
      {
        path: "operator",
        element: <OperatorLayout />,
        children: [
          { index: true, element: <ReviewQueuePage /> },
          { path: "alerts", element: <AlertsPage /> },
          { path: "tenants", element: <TenantsPage /> },
          { path: "tenants/:tenantId", element: <TenantDetailPage /> },
          { path: "web", element: <GlobalWebPage /> },
          { path: "api", element: <GlobalApiPage /> },
          { path: "compare", element: <ComparePage /> },
          { path: "api-keys", element: <ApiKeysPage /> },
          { path: "ai", element: <OperatorMcpPage /> },
          { path: "audit", element: <AuditPage /> },
          { path: "settings", element: <SettingsPage /> },
        ],
      },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
