// settings of one run. the go test in this directory (stack_test.go, make
// console-e2e) starts every service and passes where each one listens; the
// specs never guess an address.

function required(name: string): string {
  const v = process.env[name];
  if (v === undefined || v === "") {
    throw new Error(`missing env ${name}; run the suite through make console-e2e`);
  }
  return v;
}

function port(name: string): number {
  const n = Number(required(name));
  if (!Number.isInteger(n) || n <= 0 || n > 65535) throw new Error(`env ${name} is not a port`);
  return n;
}

export const BASE_DOMAIN = required("CE2E_BASE_DOMAIN");
export const APP = `https://app.${BASE_DOMAIN}`;
export const INGEST = `https://in.${BASE_DOMAIN}`;
// the github oauth fixture as the browser and the control plane reach it
export const OAUTH = required("CE2E_OAUTH_URL");
// the fixture's control api, from the test process only
export const OAUTH_CTL = required("CE2E_OAUTH_CTL");

// browser proxy and the tenant site control api, both on loopback
export const PROXY_PORT = port("CE2E_PROXY_PORT");
export const SITE_CTL_PORT = port("CE2E_SITE_CTL_PORT");

// "host:port" -> "127.0.0.1:port" of the published container port
export function routes(): Record<string, string> {
  const parsed: unknown = JSON.parse(required("CE2E_ROUTES"));
  if (typeof parsed !== "object" || parsed === null)
    throw new Error("CE2E_ROUTES is not an object");
  return parsed as Record<string, string>;
}

// output directory for screenshots and notes (gitignored scratch area)
export const OUT = required("CE2E_OUT");

// tenant-side hosts served by the local site server. they live under another
// reserved .example name, so the tracker's requests are cross-site as in
// production, and so is any page a test uses as a foreign site.
export const TENANT_DOMAIN = "sa-tenant.example";
// every host under TENANT_DOMAIN is a tenant site; each test run picks a
// fresh one, so a retried test never meets a site an earlier attempt registered
export function isTenantHost(host: string): boolean {
  return (
    /^[a-z0-9-]+$/.test(host.slice(0, -TENANT_DOMAIN.length - 1)) &&
    host.endsWith(`.${TENANT_DOMAIN}`)
  );
}
export const REFERRER_HOST = "news.sa-referrer.example";
export const REFERRER_ORIGIN = `https://${REFERRER_HOST}`;
// a foreign site for cross-site checks (csrf)
export const EVIL_HOST = "evil.sa-attacker.example";
export const EVIL_ORIGIN = `https://${EVIL_HOST}`;
export const LOCAL_HOSTS = [`*.${TENANT_DOMAIN}`, REFERRER_HOST, EVIL_HOST];

// the stack's loopback control api that starts an example app with a token
// a spec created (stack_test.go AppsControl)
export const APPS_CTL = required("CE2E_APPS_CTL");
