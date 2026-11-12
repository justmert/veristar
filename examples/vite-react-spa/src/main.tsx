import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Link, Outlet, useParams } from "react-router";
import { RouterProvider } from "react-router/dom";

declare global {
  interface Window {
    umami?: {
      track: (
        name: string,
        data?: Record<string, string | number | boolean | null>,
      ) => Promise<void>;
    };
  }
}

function Layout() {
  return (
    <>
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
      <main>
        <Outlet />
      </main>
    </>
  );
}

function Home() {
  return (
    <>
      <h1>SPA example</h1>
      <p>Client-side routing. Every route change is one pageview.</p>
      <button
        type="button"
        id="signup"
        data-umami-event="signup-click"
        data-umami-event-plan="free"
      >
        Sign up
      </button>{" "}
      <button
        type="button"
        id="manual"
        // the tracker may be blocked by the visitor, so the call is optional
        onClick={() => void window.umami?.track("manual-call", { source: "vite-react-spa" })}
      >
        Track by call
      </button>
    </>
  );
}

function Pricing() {
  return <h1>Pricing</h1>;
}

function Doc() {
  const { slug } = useParams();
  return <h1>Docs: {slug}</h1>;
}

const router = createBrowserRouter([
  {
    path: "/",
    Component: Layout,
    children: [
      { index: true, Component: Home },
      { path: "pricing", Component: Pricing },
      { path: "docs/:slug", Component: Doc },
    ],
  },
]);

const root = document.getElementById("root");
if (!root) throw new Error("#root is missing from index.html");
createRoot(root).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
