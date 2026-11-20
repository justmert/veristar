---
title: "Tenant onboarding"
sidebarTitle: "Overview"
description: "Guides for adding web analytics to a site and API analytics to a backend, with code taken from tested examples."
icon: "compass"
---

These guides show you how to add web analytics to a site, API analytics to a backend, or both.
Start with the quickstart for your kind of project, then open the guide for your framework.

Every code block in these guides comes from tested code, checked by `make docs-check`: example
code is copied from a working example under [`examples/`](https://github.com/justmert/veristar/tree/main/examples),
and each script tag is built by the console's own snippet code for the example site. The guides
show exactly the code the test suites run. Examples use
the reserved domain `analytics.example`: the gateway is `https://in.analytics.example`, the OTLP
endpoint `https://otlp.analytics.example` and the console `https://app.analytics.example`. The
console shows the real values of the service you use.

To sign in with GitHub, a Stellar wallet or a passkey, and to link them to one account, see
[Signing in](sign-in.md).

## Web analytics

<CardGroup cols={2}>
  <Card horizontal title="Quickstart" icon="globe" className="tone-web" href="/onboarding/web/quickstart">
    Every site: register it, paste one tag, see the data
  </Card>
  <Card horizontal title="Static HTML" icon="html5" iconType="brands" className="tone-web" href="/onboarding/web/static-html">
    Hand-written pages and static site generators
  </Card>
  <Card horizontal title="Vite and React" icon="react" iconType="brands" className="tone-web" href="/onboarding/web/vite-react">
    A client-routed React app
  </Card>
  <Card horizontal title="Vue" icon="vuejs" iconType="brands" className="tone-web" href="/onboarding/web/vue">
    A Vue app with Vue Router
  </Card>
  <Card horizontal title="Astro" icon="meteor" className="tone-web" href="/onboarding/web/astro">
    Astro, static or server rendered
  </Card>
  <Card horizontal title="SvelteKit" icon="fire" className="tone-web" href="/onboarding/web/sveltekit" />
  <Card horizontal title="React Router with server rendering" icon="route" className="tone-web" href="/onboarding/web/react-router">
    React Router in framework mode
  </Card>
  <Card horizontal title="Next.js" icon="n" className="tone-web" href="/onboarding/web/nextjs">
    Next.js with the App Router
  </Card>
  <Card horizontal title="Docusaurus" icon="book-open" className="tone-web" href="/onboarding/web/docusaurus">
    A Docusaurus documentation site
  </Card>
  <Card horizontal title="VitePress" icon="book" className="tone-web" href="/onboarding/web/vitepress">
    A VitePress documentation site
  </Card>
  <Card horizontal title="Mintlify" icon="leaf" className="tone-web" href="/onboarding/web/mintlify">
    A documentation site hosted by Mintlify
  </Card>
  <Card horizontal title="npm package" icon="npm" iconType="brands" className="tone-web" href="/onboarding/web/npm">
    Bundling the tracker into your app instead of a script tag
  </Card>
  <Card horizontal title="Self-hosting sdk.js" icon="hard-drive" className="tone-web" href="/onboarding/web/self-hosting">
    Serving the tracker file from your own domain
  </Card>
  <Card horizontal title="Content security policy" icon="shield-halved" className="tone-web" href="/onboarding/web/content-security-policy">
    Sites that send a CSP header
  </Card>
  <Card horizontal title="Upgrading the tracker" icon="circle-up" className="tone-web" href="/onboarding/web/upgrading">
    Moving to a newer pinned version
  </Card>
  <Card horizontal title="Consent" icon="user-check" className="tone-web" href="/onboarding/web/consent">
    Starting the tracker only after the visitor agrees
  </Card>
  <Card horizontal title="Custom events" icon="hand-pointer" className="tone-web" href="/onboarding/web/custom-events">
    Counting clicks and actions of your own
  </Card>
  <Card horizontal title="Wallets Kit" icon="wallet" className="tone-web" href="/onboarding/web/wallets-kit">
    Wallet connect events from the Stellar Wallets Kit
  </Card>
</CardGroup>

## API analytics

<CardGroup cols={2}>
  <Card horizontal title="Quickstart" icon="server" className="tone-api" href="/onboarding/api/quickstart">
    Every backend: create a token, add a second exporter, see the data
  </Card>
  <Card horizontal title="Node.js" icon="node-js" iconType="brands" className="tone-api" href="/onboarding/api/node">
    Express and other Node HTTP servers
  </Card>
  <Card horizontal title="Go" icon="golang" iconType="brands" className="tone-api" href="/onboarding/api/go">
    `net/http`
  </Card>
  <Card horizontal title="Python" icon="python" iconType="brands" className="tone-api" href="/onboarding/api/python">
    FastAPI, including `OTEL_SEMCONV_STABILITY_OPT_IN`
  </Card>
  <Card horizontal title="Rust" icon="rust" iconType="brands" className="tone-api" href="/onboarding/api/rust">
    actix-web
  </Card>
  <Card horizontal title="Java" icon="java" iconType="brands" className="tone-api" href="/onboarding/api/java">
    Spring Boot and other JVM services, with the OpenTelemetry Java agent
  </Card>
  <Card horizontal title="Stellar Anchor Platform" icon="anchor" className="tone-api" href="/onboarding/api/anchor-platform">
    The Anchor Platform's SEP server and platform server
  </Card>
  <Card horizontal title="Setting http.route" icon="signs-post" className="tone-api" href="/onboarding/api/http-route">
    Making requests group by route instead of by raw path
  </Card>
  <Card horizontal title="Fan-out from your own Collector" icon="share-nodes" className="tone-api" href="/onboarding/api/collector-fanout">
    Apps that already export to an OpenTelemetry Collector
  </Card>
  <Card horizontal title="What gets dropped" icon="filter" className="tone-api" href="/onboarding/api/what-gets-dropped">
    Which of your data the platform keeps and which it discards
  </Card>
  <Card horizontal title="Troubleshooting" icon="screwdriver-wrench" className="tone-api" href="/onboarding/api/troubleshooting">
    No data, refused requests, odd numbers
  </Card>
</CardGroup>

## Reports

<CardGroup cols={1}>
  <Card horizontal title="Share a signed report" icon="file-signature" className="tone-report" href="/onboarding/signed-reports">
    A signed, shareable report of your usage for a grant program, an investor or a partner, verifiable by anyone
  </Card>
</CardGroup>

## How the guides are tested

Each guide is backed by an example that runs in the test suites:

- The static HTML, Vite and React, Vue, Astro, SvelteKit, React Router, Next.js, Docusaurus,
  VitePress and Mintlify guides, and the consent, self-hosting, content security policy and custom
  event guides, are backed by example sites that run in the browser test matrix (`tests/e2e`:
  Chromium, Firefox and WebKit) against a test gateway, with the current tracker release. The Mintlify
  example runs in Mintlify's local preview, `mint dev`.
- The tracker's own tests (`packages/tracker/test`) cover the behaviour each newer release adds,
  such as consent mode, visibility and long single-page sessions, and check that every released
  file rebuilds byte for byte from source.
- The Wallets Kit guide is backed by `TestWalletsKit` in `tests/webpath`, which runs the full chain
  into Umami.
- The API guides are backed by `tests/otelapps`, which runs the five example apps, the Stellar
  Anchor Platform and the fan-out collector against stock collectors, and, with the `apipath` build tag, against the platform's own
  collector.

## Help

Ask questions and report problems on the public issue tracker. [Support](../support.md) describes
the forms and the response times.

To delete your console account, open the account menu (your name in the sidebar), choose
**Account** and use **Delete account**. Hand over ownership of any project you own alone first.
What the deletion removes is in [Erasure workflow](../compliance/erasure.md#7-deleting-your-console-account).
