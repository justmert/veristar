import type { ReactNode } from "react";
import {
  isRouteErrorResponse,
  Link,
  Links,
  Meta,
  Outlet,
  Scripts,
  ScrollRestoration,
  useRouteLoaderData,
} from "react-router";
import type { Route } from "./+types/root";
import { trackerTag } from "./tracker.server";

export function loader() {
  return { tracker: trackerTag() };
}

export function Layout({ children }: { children: ReactNode }) {
  // undefined only when the root loader itself failed; the error page is then
  // rendered without the tracker rather than with a guessed tag
  const data = useRouteLoaderData<typeof loader>("root");
  return (
    <html lang="en">
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
        {data ? (
          <script
            defer
            src={data.tracker.src}
            integrity={data.tracker.integrity}
            crossOrigin="anonymous"
            data-website-id={data.tracker.siteId}
          />
        ) : null}
      </head>
      <body>
        <nav>
          <Link to="/" id="nav-home">
            Home
          </Link>{" "}
          <Link to="/pricing" id="nav-pricing">
            Pricing
          </Link>{" "}
          <Link to="/docs/getting-started" id="nav-docs">
            Docs
          </Link>
        </nav>
        <main>{children}</main>
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  return <Outlet />;
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  const message = isRouteErrorResponse(error) ? `${error.status} ${error.statusText}` : "Error";
  return <h1>{message}</h1>;
}
