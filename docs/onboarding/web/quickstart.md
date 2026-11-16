---
title: "Web analytics quickstart"
sidebarTitle: "Quickstart"
description: "Register a site, paste one script tag and see pageviews, visitors and referrers in your dashboard."
---

From a fresh site to data in your dashboard: register the site, paste one script tag, open the
dashboard. Pageviews, visitors, visits, visit duration, bounce rate, referrers, countries, devices
and browsers are counted without any code beyond the tag.

The examples on this page use the reserved domain `analytics.example`: the console at
`https://app.analytics.example`, the gateway at `https://in.analytics.example` and the site id
`3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c`. The console shows the real values for your site; copy them
from there, not from this page.

## 1. Enroll your project

Sign in to the console with GitHub or a Stellar wallet ([Signing in](../sign-in.md)). Choose **Enroll a project** and fill in the project name, its
website and a short description. Depending on how the operators run the service, the project is
active at once or waits for an operator's review; the console shows which. Sites can be added
while a project waits, but they receive data only once it is active.

Add everyone who needs to see the dashboards under the project's **Members**.

## 2. Add a site

Open the project's **Sites** page and choose **Add a site**:

- **Name**: anything that tells your sites apart.
- **Domain**: the domain the site runs on, for example `docs.example.org`.
- **Allowed origins**: one per line, up to 20. Events are accepted only from pages on these
  origins, which must be the domain or its subdomains over https. Plain http is allowed only for
  `localhost` and `127.0.0.1`: add for example `http://localhost:5173` (with your dev server's
  port) to test from your machine.

A site whose domain belonged to a removed project is marked **In review** and waits for an
operator before it receives data.

The gateway refuses events from any other origin, so a copied tag on someone else's site does not
count towards yours.

## 3. Copy the tag

Choose **Snippet** next to the site. The console shows the tag for the newest tracker version.
The guides use version 1.1.0, which looks like this:

{/* snippet version=1.1.0 */}
```html
<script defer src="https://in.analytics.example/v/1.1.0/sdk.js" integrity="sha384-lxlitLtIMAeT/K3ieev+MeMGxZe7oceuWLOy8GZAjK7TMKS2Uh9UPmfGLBczUPsq" crossorigin="anonymous" data-website-id="3f6a0c2e-9b1d-4e7a-8c5f-1d2e3f4a5b6c"></script>
```

The tag pins one tracker version. The `integrity` value makes the browser refuse the file if a
single byte differs, so the code on your pages never changes unless you change the tag
([upgrading](upgrading.md) explains how). Keep every attribute as it is.

## 4. Paste it into every page

Put the tag in the `<head>` of every page, once. Where that is depends on your framework:

| Framework | Where the tag goes | Guide |
|---|---|---|
| Hand-written HTML, static site generators | the `<head>` of each page or of the shared layout | [static-html.md](static-html.md) |
| Vite with React | `index.html` | [vite-react.md](vite-react.md) |
| Vue | `index.html` | [vue.md](vue.md) |
| Astro | the layout every page uses | [astro.md](astro.md) |
| SvelteKit | `src/app.html` | [sveltekit.md](sveltekit.md) |
| React Router in framework mode | `app/root.tsx` | [react-router.md](react-router.md) |
| Next.js with the App Router | `app/layout.js` or `app/layout.tsx` | [nextjs.md](nextjs.md) |

Client-side route changes (`history.pushState`, `replaceState`, back and forward) are counted by
the tracker on its own. No router hook is needed.

To bundle the tracker with your app instead, see [npm.md](npm.md). If your site sends a
`Content-Security-Policy` header, read [content-security-policy.md](content-security-policy.md)
first, or the browser will block the tracker.

## 5. Check that it works

Open your site with the browser's developer tools on the Network tab and load a page. The Network
tab shows three requests to the gateway:

| Request | Answer | What it is |
|---|---|---|
| `GET /v/1.1.0/sdk.js` | 200 | the tracker file |
| `GET /v1/c?w=<site id>` | 200 | a challenge, once per page load |
| `POST /v1/e` | 202 | the pageview, accepted and queued |

If the tracker file is missing or the event answers something else, see
[Troubleshooting](#troubleshooting) below.

## 6. Open the dashboard

On the project's **Overview** page, choose **Open web dashboard** next to the site. It opens the
site's dashboard in Umami, signed in as you. New events appear within a few seconds. Members see
the dashboard read only: Umami still shows its Save, Transfer, Reset and Delete buttons, and
pressing one fails with "Unauthorized". Settings are changed in the console instead.

Each site has a second dashboard, **Quarantine view**. Events from one network address that shows
many different visitors within an hour (offices, campuses, carrier networks and VPN exits, but also
scripted traffic) go there instead of the main dashboard. They are kept apart, never deleted.

Countries come from the DB-IP country database:
<a href='https://db-ip.com'>IP Geolocation by DB-IP</a>.

## How visitors are counted

- **No cookies and nothing stored in the browser.** The tracker reads one browser storage key, the
  opt-out flag, and stores nothing. Only if you turn on Wallets Kit capture with the script tag
  does it also read the kit's storage keys ([wallets-kit.md](wallets-kit.md)).
- **Unique visitors are unique per UTC day.** A daily secret that mixes the network address and
  browser into a visitor token is destroyed at midnight UTC, so the same person on two days counts
  twice, and visitors over a week are the sum of the daily visitors.
- **No address is stored.** The network address is used in memory only, for the country lookup,
  the daily visitor token and the gateway's rate and abuse checks. Region and city are never
  recorded.
- **Global Privacy Control is honoured.** A browser that sends it is not counted at all.
- **URLs are cleaned before they are stored.** Query parameters are removed. Campaign
  parameters (`utm_source`, `utm_medium`, `utm_campaign`, `utm_content`, `utm_term` and `ref`)
  are kept only if you turn on "Keep campaign parameters" for the site in the console; it is off
  by default. Fragments other than `#/` routes are never sent, referrers are cut to their scheme and host, and Stellar keys and
  email addresses are replaced with placeholders.

[`docs/reference/web-metrics.md`](../../reference/web-metrics.md) defines each metric exactly.

## Privacy duties of your site

You are the controller of your visitors' data; the service processes it for you. Whether your site
can start the tracker without asking the visitor first depends on the countries you serve:
[`docs/compliance/jurisdictions.md`](../../compliance/jurisdictions.md) gives the current
assessment, and [consent.md](consent.md) shows how to start the tracker only after consent. In
every jurisdiction:

- name the service and the processing in your privacy notice,
- offer a simple opt-out control ([consent.md](consent.md#an-opt-out-control) has one),
- put no personal data in URLs, page titles or event data.

## Options of the tag

All options are attributes on the tag. They use Umami's names, so Umami's documentation for them
applies.

| Attribute | Effect |
|---|---|
| `data-website-id` | required: the site id |
| `data-auto-track="false"` | load but send nothing until `umami.start()` ([consent.md](consent.md)) |
| `data-host-url` | the gateway origin, needed only when you serve `sdk.js` yourself ([self-hosting.md](self-hosting.md)) |
| `data-domains` | a comma separated list of hostnames; on any other hostname nothing is sent (useful for staging copies of the site) |
| `data-exclude-search="true"` | send paths without the query string |
| `data-exclude-hash="true"` | never send `#/` routes either |
| `data-do-not-track="true"` | also honour the older Do Not Track setting |
| `data-before-send` | the name of a global function that can change or drop each event before it is sent |
| `data-wallets-kit="true"` | count Stellar Wallets Kit events, version 1.1.0 and newer ([wallets-kit.md](wallets-kit.md)) |

Umami's `data-tag`, `identify()`, distinct ids, web vitals and error capture do not exist in this
tracker.

## Troubleshooting

| What you see | Cause and fix |
|---|---|
| `sdk.js` is blocked with an integrity error | the tag was edited or copied incompletely; copy it again from the console |
| `sdk.js` answers 410 | that tracker version was revoked; pick a current one in the console and replace the tag ([upgrading.md](upgrading.md)) |
| no request to the gateway at all | the tag is not in the page the browser received, a content blocker removed it, the browser sends Global Privacy Control, the opt-out flag is set, or `data-auto-track="false"` is set without `umami.start()` |
| the browser console reports a content security policy violation | add the gateway to your policy ([content-security-policy.md](content-security-policy.md)) |
| `POST /v1/e` answers 403 | the page's origin is not one of the site's allowed origins; add it on the **Sites** page |
| `POST /v1/e` answers 404 | the site id is wrong, or the site or project is not active yet |
| `POST /v1/e` answers 429 | the site sent more than the gateway's rate limit; it recovers by itself |
| events arrive but the dashboard stays empty | look at the quarantine view; on a shared network your own test visits can land there |
