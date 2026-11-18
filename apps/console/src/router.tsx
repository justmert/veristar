import { Mark } from "@veristar/brand/mark";
import { createBrowserRouter, type LoaderFunctionArgs, redirect } from "react-router";
import { ApiError } from "@/api/client";
import { meQuery } from "@/api/queries";
import { AppShell } from "@/pages/app-shell";
import { AccountPage } from "@/pages/home/account";
import { EnrollPage } from "@/pages/home/enroll";
import { ProjectsPage } from "@/pages/home/projects";
import { OnchainPage } from "@/pages/onchain";
import { AlertsPage } from "@/pages/operator/alerts";
import { AuditPage } from "@/pages/operator/audit";
import { ComparePage } from "@/pages/operator/compare";
import { AccessKeysPage } from "@/pages/operator/keys";
import { OperatorLayout } from "@/pages/operator/layout";
import { OperatorsPage } from "@/pages/operator/operators";
import { OperatorProjectPage } from "@/pages/operator/project";
import { OperatorProjectsPage } from "@/pages/operator/projects";
import { ReviewQueuePage } from "@/pages/operator/review";
import { RulesPage } from "@/pages/operator/rules";
import { SettingsPage } from "@/pages/operator/settings";
import { AiPage } from "@/pages/project/ai";
import { ApiPage } from "@/pages/project/api";
import { ConnectApiPage } from "@/pages/project/connect-api";
import { MembersPage } from "@/pages/project/members";
import { OverviewPage } from "@/pages/project/overview";
import { PrivacyPage } from "@/pages/project/privacy";
import { ScfReviewPage } from "@/pages/project/scf";
import { SiteSettingsPage } from "@/pages/project/site-settings";
import { SiteSetupPage } from "@/pages/project/site-setup";
import { SitesPage } from "@/pages/project/sites";
import { WebPage } from "@/pages/project/web";
import { ProjectLayout } from "@/pages/project-layout";
import { AiConsentPage } from "@/pages/public/ai-consent";
import { NotFoundPage, RouteErrorPage } from "@/pages/public/not-found";
import { SignInPage } from "@/pages/public/sign-in";
import { SsoErrorPage } from "@/pages/public/sso-error";
import { TermsPage } from "@/pages/public/terms";
import { PublicReportPage } from "@/pages/public-report";
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
  // an address older versions sent people to after a single sign-on
  { path: "/sso-error", element: <SsoErrorPage />, errorElement: <RouteErrorPage /> },
  { path: "/terms", element: <TermsPage />, errorElement: <RouteErrorPage /> },
  // a shared signed report: public, no session (contract 20.5)
  { path: "/r/:reportId", element: <PublicReportPage />, errorElement: <RouteErrorPage /> },
  // the mcp oauth consent screen; it signs the person in itself, so the
  // sealed request survives the github round trip (contract 12.3)
  { path: "/ai/consent", element: <AiConsentPage />, errorElement: <RouteErrorPage /> },
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
          { path: "web", element: <WebPage /> },
          { path: "api", element: <ApiPage /> },
          { path: "onchain", element: <OnchainPage /> },
          { path: "scf", element: <ScfReviewPage /> },
          { path: "sites", element: <SitesPage /> },
          { path: "sites/:siteId", element: <SiteSetupPage /> },
          { path: "sites/:siteId/settings", element: <SiteSettingsPage /> },
          { path: "connect-api", element: <ConnectApiPage /> },
          { path: "ai", element: <AiPage /> },
          { path: "members", element: <MembersPage /> },
          { path: "privacy", element: <PrivacyPage /> },
          { path: "*", element: <NotFoundPage /> },
        ],
      },
      {
        path: "operator",
        element: <OperatorLayout />,
        children: [
          { index: true, element: <ReviewQueuePage /> },
          { path: "alerts", element: <AlertsPage /> },
          { path: "projects", element: <OperatorProjectsPage /> },
          { path: "projects/:tenantId", element: <OperatorProjectPage /> },
          { path: "compare", element: <ComparePage /> },
          { path: "rules", element: <RulesPage /> },
          { path: "settings", element: <SettingsPage /> },
          { path: "operators", element: <OperatorsPage /> },
          { path: "keys", element: <AccessKeysPage /> },
          { path: "audit", element: <AuditPage /> },
          { path: "*", element: <NotFoundPage /> },
        ],
      },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
]);
