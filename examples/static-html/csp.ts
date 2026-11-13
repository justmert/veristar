// the content security policy the example server sends with every page. it
// is the policy docs/onboarding/web/content-security-policy.md recommends,
// and the browser matrix runs every static page under it.

// docs:start policy
// gateway is the origin the tracker tag loads from, for example
// https://in.analytics.example. sdk.js is a script from that origin, and
// its challenge request and events are fetch calls to the same origin.
export function contentSecurityPolicy(gateway: string): string {
  return [
    "default-src 'self'",
    `script-src 'self' ${gateway}`,
    `connect-src 'self' ${gateway}`,
    "style-src 'self'",
    "img-src 'self'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    "object-src 'none'",
  ].join("; ");
}
// docs:end policy
