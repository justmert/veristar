// shared table of the browser matrix: hostnames, local ports and the site ids
// the test-only gateway fixture registers. the ids are fixed test values so that
// the playwright config, every worker and every example server agree on them.

// in.sa-test.example is the gateway test host: .example is reserved by rfc 2606,
// so the name can never resolve to a real host.
export const GATEWAY_HOST = "in.sa-test.example";
export const GATEWAY_ORIGIN = `https://${GATEWAY_HOST}`;

// the release the matrix runs against.
export const TRACKER_VERSION = "1.0.0";
export const SDK_PATH = `/v/${TRACKER_VERSION}/sdk.js`;

// tenant sites live under a different reserved .example name so the tracker's
// requests are cross-site, as they are in production (a tenant site never shares
// a registrable domain with the gateway).
export const TENANT_DOMAIN = "sa-tenant.example";

export const PORTS = {
  // forward proxy every browser is pointed at; it is how the fake hostnames resolve
  proxy: 27080,
  // tls front behind the proxy, terminates https for every hostname
  tls: 27443,
  // control api used by the test runner only, never by the browser
  control: 27090,
} as const;

export interface SiteDef {
  key:
    | "static-html"
    | "vite-react-spa"
    | "react-router-ssr"
    | "astro-ssr"
    | "vue-spa"
    | "sveltekit"
    | "nextjs"
    | "docusaurus"
    | "vitepress"
    | "mintlify"
    | "wallets-kit-spa";
  host: string;
  origin: string;
  port: number;
  siteId: string;
  // workspace package that serves the site
  pkg: string;
  // set for an example that is a standalone npm project outside the pnpm
  // workspace: its directory, relative to the repository root
  npmDir?: string;
  // the address the site's server listens on, when it is not 127.0.0.1
  listenHost?: string;
  // a link on the home page that leads to another page of the site
  navSelector: string;
}

function site(
  key: SiteDef["key"],
  label: string,
  port: number,
  siteId: string,
  navSelector: string,
  npmDir?: string,
  listenHost?: string,
): SiteDef {
  const host = `${label}.${TENANT_DOMAIN}`;
  return {
    key,
    host,
    origin: `https://${host}`,
    port,
    siteId,
    pkg: `@veristar/example-${key}`,
    navSelector,
    ...(npmDir ? { npmDir } : {}),
    ...(listenHost ? { listenHost } : {}),
  };
}

export const SITES: readonly SiteDef[] = [
  site("static-html", "static", 27101, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e01", "#to-about"),
  site("vite-react-spa", "spa", 27102, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e02", "#nav-pricing"),
  site("react-router-ssr", "rr", 27103, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e03", "#nav-pricing"),
  site("astro-ssr", "astro", 27104, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e04", "#nav-about"),
  // the sites of the vue, sveltekit and next.js onboarding guides
  site("vue-spa", "vue", 27106, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e06", "#nav-pricing"),
  site("sveltekit", "svelte", 27107, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e07", "#nav-pricing"),
  site("nextjs", "next", 27108, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e08", "#nav-pricing"),
  // the sites of the documentation site guides. docusaurus and vitepress are
  // standalone npm projects; mintlify runs its local preview (mint dev)
  site(
    "docusaurus",
    "docusaurus",
    27109,
    "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e09",
    "#nav-pricing",
    "examples/docusaurus",
  ),
  site(
    "vitepress",
    "vitepress",
    27110,
    "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e10",
    "#nav-pricing",
    "examples/vitepress",
  ),
  // mint dev listens on localhost only (::1 or 127.0.0.1, as the resolver
  // orders them); it has no option to pick the loopback address
  site(
    "mintlify",
    "mintlify",
    27111,
    "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e11",
    "#nav-pricing",
    undefined,
    "localhost",
  ),
];

// the wallets kit example. it is not part of the browser matrix
// (SITES), which runs tracker 1.0.0; it needs 1.1.0 for data-wallets-kit and
// is built with the test wallet (pnpm build:test).
export const WALLET_SITE: SiteDef = site(
  "wallets-kit-spa",
  "wk",
  27105,
  "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e05",
  "#connect",
);
export const WALLET_TRACKER_VERSION = "1.1.0";

// every site the fixture's tls front serves
export const HOSTED_SITES: readonly SiteDef[] = [...SITES, WALLET_SITE];

export function siteByKey(key: SiteDef["key"]): SiteDef {
  const s = HOSTED_SITES.find((x) => x.key === key);
  if (!s) throw new Error(`unknown site ${key}`);
  return s;
}
