// shared table of the redirect research harness.
// the ports are a range of their own so this harness can run next to the main
// browser matrix of tests/e2e, which holds 27080, 27443 and 27090.

export const REDIRECT_PORTS = {
  // forward proxy every browser is pointed at; it is how the fake names resolve
  proxy: 27280,
  // tls front behind the proxy for every name below
  tls: 27643,
  // control api for the test runner only
  control: 27290,
} as const;

// the new gateway after a domain change: serves the released sdk.js with the
// production headers and runs the fixture gateway's checks
export const NEW_HOST = "in.sa-new.example";
// the old gateway host answering 308 to NEW_HOST, with
// access-control-allow-origin: * on the redirect response itself
export const OLD_HOST = "in.sa-old.example";
// the same 308 without any cors header, as a plain `redir` would answer
export const OLD_BARE_HOST = "in.sa-oldbare.example";
// the tenant page carrying the snippet, on a registrable domain of its own so
// every gateway request is cross-site, as in production
export const TENANT_HOST = "site.sa-rtenant.example";
export const TENANT_ORIGIN = `https://${TENANT_HOST}`;

export const SITE_ID = "4f1c2e0a-9b7d-4c3e-8a61-2d5f7e9b0c14";
export const TRACKER_VERSION = "1.1.0";
// the right value for TRACKER_VERSION is read from the release manifest; this
// is the published value of the other release, so it is a well-formed hash
// that does not match the served bytes
export const WRONG_VERSION = "1.0.0";
