// every statement on the site, with the repository file that backs it. a
// claim that a file in the repository cannot back does not belong here:
// change the source first, then the sentence.

export type Fate = "kept" | "dropped" | "never";

export type Field = { label: string; value: string; fate: Fate };

// one example page view and one example api request. the values are
// examples (203.0.113.0/24 is a documentation range); the fates are what the
// pipeline does with each field.
// web: docs/compliance/privacy-strategy.md sections 2 and 3 (country code
// from the /24, session device and browser, referrer host only, daily
// visitor token, no cookie, address never stored).
// api: docs/onboarding/api/http-route.md (placeholders),
// docs/onboarding/api/what-gets-dropped.md and privacy-strategy.md section 2
// (spans become histograms and are dropped, logs accepted and discarded).
export const paths = {
  web: {
    tab: "Website",
    noun: "page view",
    fields: [
      { label: "Page", value: "/pricing", fate: "kept" },
      { label: "Referrer", value: "github.com, host only", fate: "kept" },
      { label: "Country", value: "country code, from 203.0.113.0/24", fate: "kept" },
      { label: "Device, browser", value: "desktop, Firefox", fate: "kept" },
      { label: "IP address", value: "203.0.113.7", fate: "dropped" },
      { label: "Visitor", value: "daily token, secret gone at 00:00 UTC", fate: "kept" },
      { label: "Cookies", value: "none", fate: "never" },
    ],
    // erasure.md section 1: visitor_today, session, site_reset
    foot: "Stored in the EU. Erasable per visitor on the day of the visit, per session or per site.",
  },
  api: {
    tab: "Backend",
    noun: "API request",
    fields: [
      { label: "Route", value: "/accounts/{account}", fate: "kept" },
      { label: "Method, status", value: "GET 200", fate: "kept" },
      { label: "Duration", value: "in a latency histogram", fate: "kept" },
      { label: "Service", value: "your service name", fate: "kept" },
      { label: "Account in path", value: "the G... account key", fate: "dropped" },
      { label: "Trace and spans", value: "used for counts, then discarded", fate: "dropped" },
      { label: "Logs", value: "accepted, then discarded", fate: "dropped" },
    ],
    // README.md (api analytics) and privacy-strategy.md section 4
    foot: "Only request counts, status codes and latency per route are stored, for at least 24 months.",
  },
} as const satisfies Record<
  "web" | "api",
  { tab: string; noun: string; fields: Field[]; foot: string }
>;

export type Trust = { title: string; detail: string; doc: string };

export const trust: Trust[] = [
  // docs/compliance/data-residency.md, declaration
  {
    title: "Hosted in the EU",
    detail: "Azure Denmark East, three zones",
    doc: "compliance/data-residency",
  },
  // LICENSE, README.md licence section, docs/adr/0003-licensing.md
  {
    title: "Apache 2.0",
    detail: "Infrastructure code included",
    doc: "adr/0003-licensing",
  },
  // docs/compliance/terms.md section 2
  {
    title: "No fees, for anyone",
    detail: "No paid tier, no ads, no resale",
    doc: "compliance/terms",
  },
  // docs/architecture.md (immutable releases with sha-384 integrity),
  // apps/console/src/pages/snippet.tsx
  {
    title: "Pinned tracker",
    detail: "One version, checked by the browser",
    doc: "security/verify-tracker",
  },
];

export type Step = { title: string; detail: string };

export const doors = {
  // README.md (for projects), apps/console/src/pages/sign-in.tsx and
  // internal/controlplane/auth/auth.go (no oauth scopes), snippet.tsx
  teams: {
    who: "For project teams",
    title: "Numbers you do not have to defend.",
    steps: [
      { title: "Sign in with GitHub and enroll.", detail: "No email, no repository access." },
      {
        title: "Paste one script tag.",
        detail: "Each site gets its own snippet, pinned to a tracker version by an integrity hash.",
      },
      {
        title: "Add your API, optionally.",
        detail: "One extra OpenTelemetry exporter, with a token from the console.",
      },
    ],
  },
  // docs/operators/reading-the-numbers.md (bands, rejected share) and
  // docs/operators/global-views.md (verified sites, stellar.toml columns)
  reviewers: {
    who: "For SCF reviewers",
    title: "One method for every project.",
    steps: [
      {
        title: "Same collection everywhere.",
        detail: "One pinned tracker, the same proof of work, the same route normalization.",
      },
      {
        title: "Inflation is held back, not hidden.",
        detail: "Quarantined and rejected traffic is counted separately and shown beside the rest.",
      },
      {
        title: "Evidence next to the numbers.",
        detail: "Verified domains and stellar.toml are shown beside each project's numbers.",
      },
    ],
  },
} satisfies Record<string, { who: string; title: string; steps: Step[] }>;

// docs/architecture.md (hosts, pipeline), privacy-strategy.md sections 2 to
// 4, docs/operators/reading-the-numbers.md (server-side posts rejected)
export const pipelines = [
  {
    title: "Web analytics",
    host: "in",
    steps: [
      {
        title: "A pinned script",
        detail: "One released version, checked against its integrity hash.",
      },
      { title: "A browser proof", detail: "A small proof of work turns away server-side posts." },
      {
        title: "Checked, then forgotten",
        detail:
          "Origin, proof and rate limits are checked; the address is shortened for the country, then dropped.",
      },
      {
        title: "Queued, then counted",
        detail: "A replicated queue holds events up to 72 hours on the way to the dashboard.",
      },
    ],
  },
  {
    title: "API analytics",
    host: "otlp",
    steps: [
      { title: "Export twice", detail: "Your app keeps its own tools and adds one exporter." },
      {
        title: "Keep only the metric",
        detail: "Spans become counts and latency; traces, spans and logs are dropped.",
      },
      {
        title: "Routes normalized",
        detail: "Ids, Stellar addresses and hashes become placeholders.",
      },
      {
        title: "Kept as aggregates",
        detail: "Rolled up by minute, hour and day; hourly rows are kept at least 24 months.",
      },
    ],
  },
] satisfies { title: string; host: string; steps: Step[] }[];

// docs/compliance/privacy-strategy.md sections 2, 3 and 6,
// docs/compliance/data-residency.md, docs/compliance/erasure.md,
// docs/compliance/jurisdictions.md (two modes), README.md
export const privacy: { term: string; detail: string }[] = [
  {
    term: "Cookies",
    detail: "None. The tracker sets no cookie and never writes browser storage.",
  },
  {
    term: "IP addresses",
    detail:
      "In memory for the request only, shortened to /24 or /48 for the country lookup, never logged, queued or stored.",
  },
  {
    term: "Visitor counts",
    detail:
      "A secret that lasts one UTC day and is destroyed at midnight, so no one can follow a visitor from one day to the next.",
  },
  {
    term: "Where data lives",
    detail: "Microsoft Azure, Denmark East (EU). Analytics data does not leave the EU.",
  },
  {
    term: "Erasure",
    detail:
      "Self-service in the console: one visitor on the day of the visit, one session, or a whole site.",
  },
  {
    term: "Objection",
    detail:
      "The tracker sends nothing when the browser signals Global Privacy Control or the visitor opted out.",
  },
  {
    term: "Consent mode",
    detail:
      "Whether a site may measure without asking depends on the country. Where it may not, one attribute holds the tracker until your banner starts it.",
  },
];

export const privacyDocs = [
  { label: "Privacy strategy", doc: "compliance/privacy-strategy" },
  { label: "Jurisdictions and consent mode", doc: "compliance/jurisdictions" },
  { label: "Data residency", doc: "compliance/data-residency" },
];

// docs/sla.md summary table; docs/support.md
export const targets = [
  {
    value: "99.99%",
    title: "Ingestion availability",
    detail: "Target, measured monthly by an external probe every 30 s",
  },
  { value: "99.90%", title: "Dashboard availability", detail: "Target, measured the same way" },
  {
    value: "24 h",
    title: "Critical patches",
    detail: "Target to patch a critical or high vulnerability",
  },
  { value: "5 days", title: "Support response", detail: "Target, in business days" },
];

// docs/handover/plan.md (window, shadow period, assets) and
// .scratch/rfp.md section 3.4
export const handover = [
  { when: "Month 0", what: "The 12-month service window starts with the final tranche." },
  {
    when: "Month 6 to 12",
    what: "Handover at SCF's discretion, after a shadow period of at least four weeks.",
  },
  {
    when: "Transfer",
    what: "Repository, domain, cloud resources, data, DNS, and every secret rotated.",
  },
  { when: "Done", what: "A community-appointed operator runs the full system on its own." },
];
