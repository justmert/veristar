// shared table of the browser matrix: hostnames, local ports and the site ids
// the test-only gateway fixture registers. the ids are fixed test values so that
// the playwright config, every worker and every example server agree on them.

// the design names in.sa-test.example as the reserved gateway test host.
export const GATEWAY_HOST = "in.sa-test.example";
export const GATEWAY_ORIGIN = `https://${GATEWAY_HOST}`;

// the release the matrix runs against. this stage is done when 1.0.0 passes.
export const TRACKER_VERSION = "1.0.0";
export const SDK_PATH = `/v/${TRACKER_VERSION}/sdk.js`;

// tenant sites live under a different reserved .example name so the tracker's
// requests are cross-site, as they are in production (a tenant site never shares
// a registrable domain with the gateway).
export const TENANT_DOMAIN = "sa-tenant.example";

export const PORTS = {
  // forward proxy every browser is pointed at; it is how the fake hostnames resolve
  proxy: 47080,
  // tls front behind the proxy, terminates https for every hostname
  tls: 47443,
  // control api used by the test runner only, never by the browser
  control: 47090,
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
    | "wallets-kit-spa";
  host: string;
  origin: string;
  port: number;
  siteId: string;
  // workspace package that serves the site
  pkg: string;
  // a link on the home page that leads to another page of the site
  navSelector: string;
}

function site(
  key: SiteDef["key"],
  label: string,
  port: number,
  siteId: string,
  navSelector: string,
): SiteDef {
  const host = `${label}.${TENANT_DOMAIN}`;
  return {
    key,
    host,
    origin: `https://${host}`,
    port,
    siteId,
    pkg: `@stellar-analytics/example-${key}`,
    navSelector,
  };
}

export const SITES: readonly SiteDef[] = [
  site("static-html", "static", 47101, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e01", "#to-about"),
  site("vite-react-spa", "spa", 47102, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e02", "#nav-pricing"),
  site("react-router-ssr", "rr", 47103, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e03", "#nav-pricing"),
  site("astro-ssr", "astro", 47104, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e04", "#nav-about"),
  // added in this stage for the vue, sveltekit and next.js onboarding guides
  site("vue-spa", "vue", 47106, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e06", "#nav-pricing"),
  site("sveltekit", "svelte", 47107, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e07", "#nav-pricing"),
  site("nextjs", "next", 47108, "0b6f2a5e-3c1d-4f7a-9e2b-5d8c1a4f6e08", "#nav-pricing"),
];

// the this stage wallets kit example. it is not part of the this stage matrix
// (SITES), which runs tracker 1.0.0; it needs 1.1.0 for data-wallets-kit and
// is built with the test wallet (pnpm build:test).
export const WALLET_SITE: SiteDef = site(
  "wallets-kit-spa",
  "wk",
  47105,
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
