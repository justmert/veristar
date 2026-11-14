import { Mark } from "@veristar/brand/mark";
import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { siblingUrl } from "@/lib/hosts";

// the frame of the pages a person sees outside the signed-in shell (sign-in,
// the dashboard error, the terms, the ai consent screen): the mark at the
// top left, the page content, and a quiet footer with the service links
export function PublicFrame({
  children,
  header,
  footer = true,
  center = true,
  className,
}: {
  children: ReactNode;
  // extra content at the right end of the header row
  header?: ReactNode;
  footer?: boolean;
  // centre the content in the remaining height (sign-in, errors); the terms
  // page lays its own columns out from the top
  center?: boolean;
  className?: string;
}) {
  return (
    <div className="flex min-h-dvh flex-col bg-ground text-fg">
      <header className="flex h-16 items-center gap-4 px-4 sm:px-8">
        <BrandLink />
        {header ? <div className="ml-auto flex items-center gap-2">{header}</div> : null}
      </header>
      <main
        id="main"
        className={cn(
          "flex flex-1 animate-rise-in px-4 pb-12",
          center ? "items-center justify-center pt-6 sm:pt-8" : "flex-col pt-6 sm:pt-10",
          className,
        )}
      >
        {children}
      </main>
      {footer ? <PublicFooter /> : null}
    </div>
  );
}

export function BrandLink() {
  return (
    <Link
      to="/"
      aria-label="Veristar"
      className="-mx-1 flex items-center gap-2 rounded-md px-1 py-1 text-fg focus-ring"
    >
      <Mark className="size-6" />
      <span className="text-title tracking-tight">Veristar</span>
    </Link>
  );
}

// docs and status exist only on the production host names, so a link is
// shown only where it points at a real address
function PublicFooter() {
  const docs = siblingUrl(window.location.hostname, "docs");
  const status = siblingUrl(window.location.hostname, "status");
  const link =
    "rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring max-sm:py-2";
  return (
    <footer className="flex flex-wrap items-center gap-x-6 gap-y-1 border-t border-line px-4 py-4 text-small sm:px-8">
      {docs ? (
        <a href={docs} className={link}>
          Docs
        </a>
      ) : null}
      {status ? (
        <a href={status} className={link}>
          Service status
        </a>
      ) : null}
      <Link to="/terms" className={link}>
        Terms
      </Link>
      <span className="text-fg-3 sm:ml-auto">No fees, no ads, no cookies on your visitors.</span>
    </footer>
  );
}
