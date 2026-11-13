import Link from "next/link";
import { trackerTag } from "./tracker.js";

// read at request time, so the environment of the running server applies
export const dynamic = "force-dynamic";

export default function RootLayout({ children }) {
  const tracker = trackerTag();
  return (
    <html lang="en">
      <head>
        {/* docs:start head */}
        {/* a plain script element, not next/script: the tag must reach the
            browser exactly as the console wrote it, with its integrity value */}
        <script
          defer
          src={tracker.src}
          integrity={tracker.integrity}
          crossOrigin="anonymous"
          data-website-id={tracker.siteId}
        />
        {/* docs:end head */}
      </head>
      <body>
        <nav>
          <Link href="/" id="nav-home">
            Home
          </Link>{" "}
          <Link href="/pricing" id="nav-pricing">
            Pricing
          </Link>{" "}
          <Link href="/docs/getting-started" id="nav-docs">
            Docs
          </Link>
        </nav>
        <main>{children}</main>
      </body>
    </html>
  );
}
