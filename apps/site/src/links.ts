// every host the site links to is a sibling of the one serving it:
// <domain> or www.<domain> serves the site, app.<domain> the console,
// docs.<domain> the documentation and status.<domain> the status page.
// the domain is read from the address bar, so the same build works on any
// domain the service is handed over to (docs/handover/plan.md section 5).

export type Links = {
  domain: string | null;
  signIn: string | null;
  docs: (path?: string) => string | null;
  status: string | null;
  // the source repository, linked only once it is public: the build sets
  // VITE_REPO_URL then. unset, every repository link stays hidden.
  repo: string | null;
};

// www.<domain> and a two-label <domain> are the site's own hosts. anything
// else (localhost, an address, a preview host) names no domain, because a
// sibling guessed from it would point somewhere that does not exist.
export function siteDomain(hostname: string): string | null {
  const host = hostname.toLowerCase().replace(/\.$/, "");
  if (!host || host === "localhost" || /^[\d.]+$/.test(host) || host.includes(":")) return null;
  const bare = host.startsWith("www.") ? host.slice(4) : host;
  return bare.split(".").length === 2 ? bare : null;
}

function trimOrigin(v: unknown): string | null {
  if (typeof v !== "string" || !v.trim()) return null;
  return v.trim().replace(/\/+$/, "");
}

export function links(hostname: string, consoleOrigin: unknown, repoUrl?: unknown): Links {
  const domain = siteDomain(hostname);
  const fallback = trimOrigin(consoleOrigin);
  const signIn = domain ? `https://app.${domain}/sign-in` : fallback ? `${fallback}/sign-in` : null;
  return {
    domain,
    signIn,
    docs: (path = "") => (domain ? `https://docs.${domain}/${path.replace(/^\//, "")}` : null),
    status: domain ? `https://status.${domain}` : null,
    repo: trimOrigin(repoUrl),
  };
}

export const site: Links = links(
  typeof window === "undefined" ? "" : window.location.hostname,
  import.meta.env.VITE_CONSOLE_ORIGIN,
  import.meta.env.VITE_REPO_URL,
);
