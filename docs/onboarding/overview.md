---
title: "Tenant onboarding"
sidebarTitle: "Overview"
description: "Guides for adding web analytics to a site and API analytics to a backend, with code taken from tested examples."
---

Guides for teams that want web analytics for a site, API analytics for a backend, or both. Start
with the quickstart for your kind of project, then open the guide for your framework.

Every code block in these guides is copied from a working example under [`examples/`](https://github.com/justmert/veristar/tree/main/examples)
by a test (`make docs-check`), so the guides show exactly the code the test suites run. Examples use
the reserved domain `analytics.example`: the gateway is `https://in.analytics.example`, the OTLP
endpoint `https://otlp.analytics.example` and the console `https://app.analytics.example`. The
console shows the real values of the service you use.

## Web analytics

| Guide | For |
|---|---|
| [Quickstart](web/quickstart.md) | every site: register it, paste one tag, see the data |
| [Static HTML](web/static-html.md) | hand-written pages and static site generators |
| [Vite and React](web/vite-react.md) | a client-routed React app |
| [Vue](web/vue.md) | a Vue app with Vue Router |
| [Astro](web/astro.md) | Astro, static or server rendered |
| [SvelteKit](web/sveltekit.md) | SvelteKit |
| [React Router with server rendering](web/react-router.md) | React Router in framework mode |
| [Next.js](web/nextjs.md) | Next.js with the App Router |
| [npm package](web/npm.md) | bundling the tracker into your app instead of a script tag |
| [Self-hosting sdk.js](web/self-hosting.md) | serving the tracker file from your own domain |
| [Content security policy](web/content-security-policy.md) | sites that send a CSP header |
| [Upgrading the tracker](web/upgrading.md) | moving to a newer pinned version |
| [Consent](web/consent.md) | starting the tracker only after the visitor agrees |
| [Custom events](web/custom-events.md) | counting clicks and actions of your own |
| [Wallets Kit](web/wallets-kit.md) | wallet connect events from the Stellar Wallets Kit |

## API analytics

| Guide | For |
|---|---|
| [Quickstart](api/quickstart.md) | every backend: create a token, add a second exporter, see the data |
| [Node.js](api/node.md) | Express and other Node HTTP servers |
| [Go](api/go.md) | `net/http` |
| [Python](api/python.md) | FastAPI, including `OTEL_SEMCONV_STABILITY_OPT_IN` |
| [Rust](api/rust.md) | actix-web |
| [Setting http.route](api/http-route.md) | making requests group by route instead of by raw path |
| [Fan-out from your own Collector](api/collector-fanout.md) | apps that already export to an OpenTelemetry Collector |
| [What gets dropped](api/what-gets-dropped.md) | which of your data the platform keeps and which it discards |
| [Troubleshooting](api/troubleshooting.md) | no data, refused requests, odd numbers |

## Verification status

The web guides for static HTML, Vite and React, Vue, Astro, SvelteKit, React Router and Next.js, the
consent, self-hosting, content security policy and custom event guides are backed by example sites
that run in the browser test matrix (`tests/e2e`: Chromium, Firefox and WebKit) against a test
gateway. The Wallets Kit guide is backed by `TestWalletsKit` in `tests/webpath`, which runs the full
chain into Umami. The API guides are backed by `tests/otelapps`, which runs the four example apps
and the fan-out collector against stock collectors, and by `tests/otelapps` with the `apipath` tag,
which runs the apps against the real platform collector.

None of the guides has yet been followed on a production site. That happens in this stage of the
project plan, with three web projects and one API project.

## Help

Questions and problems go to the project's public issue tracker; [support.md](../support.md)
describes the forms and the response times.
