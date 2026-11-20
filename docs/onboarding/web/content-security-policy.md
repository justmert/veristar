---
title: "Content security policy"
description: "The Content-Security-Policy directives that let the tracker load and send events."
icon: "shield-halved"
---

If your site sends a `Content-Security-Policy` header (or a `<meta http-equiv>` policy), the browser
blocks the tracker unless the policy allows the gateway. Tested by the static example, whose server
sends the policy below with every page; the browser matrix runs all its pages under it and checks
that nothing is blocked.

## What the tracker needs

| Directive | Needs | Why |
|---|---|---|
| `script-src` | the gateway origin, for example `https://in.analytics.example` | `sdk.js` is loaded from it |
| `connect-src` | the gateway origin | the challenge (`GET /v1/c`) and the events (`POST /v1/e`) are `fetch` calls to it |

Nothing else: the tracker needs no inline script, no `eval`, no style, no image, no frame and no
worker. It uses WebCrypto in the page itself for the challenge.

## The tested policy

The example builds its policy from the gateway origin:

{/* include examples/static-html/csp.ts region=policy */}
```ts
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
```

With the example gateway that is the header

```text
Content-Security-Policy: default-src 'self'; script-src 'self' https://in.analytics.example; connect-src 'self' https://in.analytics.example; style-src 'self'; img-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'
```

Add the two gateway entries to your own policy rather than replacing it with this one; the other
directives are the example site's own.

## Notes

- **Self-hosted `sdk.js`** ([self-hosting.md](self-hosting.md)): `script-src` then needs your own
  origin (`'self'`) instead of the gateway, and `connect-src` still needs the gateway.
- **Nonces and `'strict-dynamic'`.** With a nonce-based policy, give the tag your nonce attribute;
  with `'strict-dynamic'`, the browser ignores host entries in `script-src`, so the nonce is
  required. The browser tests cover the host-based policy above.
- **Page code that calls `umami.track()`** must itself be allowed by the policy. The example keeps
  it in a file of its own ([`page.js`](https://github.com/justmert/veristar/blob/main/examples/static-html/src/page.js)) so that no
  inline script is needed.
- **Integrity is separate.** The `integrity` attribute is checked whatever the policy says; a CSP
  does not replace it, and it does not replace a CSP.

## What the browser tests check

The test page listens for `securitypolicyviolation` events, loads the home page with the policy
above, sends a pageview and both kinds of custom event, and requires that no violation was reported
and every event arrived, in Chromium, Firefox and WebKit (`tests/e2e/specs/onboarding.spec.ts`).
