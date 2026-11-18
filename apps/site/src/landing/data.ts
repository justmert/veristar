// the content of the landing page. every fact is from the docs in this
// repository; the sources are named next to each block.
import { hrefs } from "./hrefs";

// hero: eleven projects, each measured its own way. heights stay the same in
// both states; only the baseline, the angle and the style change.
export const heroBars = [
  { h: 62, dy: -34, r: -6, v: "v1" },
  { h: 38, dy: 22, r: 4, v: "v2" },
  { h: 80, dy: -12, r: -3, v: "v3" },
  { h: 45, dy: 38, r: 7, v: "v1" },
  { h: 70, dy: -26, r: -5, v: "v2" },
  { h: 30, dy: 16, r: 3, v: "v3" },
  { h: 55, dy: -42, r: -8, v: "v1" },
  { h: 88, dy: 10, r: 5, v: "v2" },
  { h: 42, dy: -22, r: -4, v: "v3" },
  { h: 66, dy: 30, r: 6, v: "v1" },
  { h: 50, dy: -8, r: -2, v: "v2" },
] as const;

// the problem: the review cycle of resources/rfp.md section 2, and veristar's fix for each step
export const reviewSteps = [
  { step: "A project assembles its own usage report", fix: "Veristar measures it for you" },
  { step: "Reviewers question the numbers", fix: "Five checks on every page view" },
  { step: "The project defends them", fix: "A signed report anyone can verify" },
  { step: "The decision stays uncertain", fix: "The same metrics for every project" },
];

export const verbs = [
  {
    who: "Your team",
    w: "Add",
    p: "Put a script tag on your site. Add an OpenTelemetry exporter to your API, and declare your Stellar accounts.",
  },
  {
    who: "Veristar",
    w: "Check",
    p: "Each event passes five checks. Personal data is removed. Every project is counted the same way.",
  },
  {
    who: "Everyone",
    w: "Read",
    p: "Your team reads a private dashboard. SCF reads totals across projects. Reviewers verify signed reports.",
  },
];

export const strata = [
  {
    name: "Web",
    from: "from a script tag",
    terms: [
      { k: "Page views", v: "Every page view event from the pinned tracker." },
      { k: "Visitors", v: "One person on one UTC day." },
      {
        k: "Visit duration and bounce rate",
        v: "From the first to the last page view of a visit.",
      },
      { k: "Referrers and countries", v: "The host that sent the visitor, and the country." },
      {
        k: "Devices, browsers and events",
        v: "Including wallet connections from the Stellar Wallets Kit.",
      },
    ],
  },
  {
    name: "API",
    from: "from OpenTelemetry",
    terms: [
      { k: "Requests", v: "Per service, method and route." },
      { k: "Routes", v: "Variable parts become placeholders, so one endpoint is one row." },
      { k: "Status codes", v: "The success and error rates." },
      { k: "Latency", v: "p50, p95 and p99 response times." },
      { k: "History", v: "Kept for at least 24 months." },
    ],
  },
  {
    name: "On-chain",
    from: "from the Stellar network",
    terms: [
      {
        k: "Operations",
        v: "Successful operations of the accounts and contracts that you declare.",
      },
      { k: "Proof of control", v: "A SEP-53 signature or a data entry. Only proved ids count." },
      { k: "Counterparties", v: "Distinct accounts that interact with yours, per day." },
      { k: "Weekly active accounts", v: "As the SCF Growth Hack rules define them." },
    ],
  },
];

// trust: docs/security/bot-and-inflation.md
export const gateNames = [
  "Pinned script",
  "Browser proof",
  "Origin",
  "Rate limits",
  "Same network",
];

export type Fate = "ok" | "no" | "hold";
export const travellers: { who: string; stop: string; at: number; fate: Fate; verdict: string }[] =
  [
    { who: "A visitor in a real browser", stop: "100%", at: 0, fate: "ok", verdict: "Counted" },
    { who: "A post from a server script", stop: "30%", at: 2, fate: "no", verdict: "Rejected" },
    {
      who: "Your tag, copied to a site you did not register",
      stop: "50%",
      at: 3,
      fate: "no",
      verdict: "Rejected",
    },
    {
      who: "Many visitors from one network",
      stop: "90%",
      at: 5,
      fate: "hold",
      verdict: "Held back",
    },
  ];

// docs/onboarding/signed-reports.md
export const reportLines = [
  { k: "Period", v: "whole UTC days, up to 366" },
  { k: "Web traffic", v: "page views, visitors, visits" },
  { k: "API requests", v: "counts, status codes, latency" },
  { k: "On-chain activity", v: "accounts and contracts you proved you control" },
  { k: "Integrity grade", v: "A, B or C, with the reasons" },
  { k: "SCF cohort", v: "your percentile for each metric" },
  { k: "Definitions", v: "the version of each rule behind the numbers" },
];

// for scf: docs/operators/overview.md
export const opGroups = [
  {
    name: "Attention",
    pages: [
      [
        "Review queue",
        "Projects that wait for approval, and active projects with a site in review. The oldest come first.",
      ],
      [
        "Alerts",
        "The live alerts of the platform, by severity. The page refreshes every 30 seconds.",
      ],
    ],
  },
  {
    name: "Projects",
    pages: [
      ["Projects", "Every project, with search, a status filter and sort."],
      [
        "Global web",
        "The web numbers of every project for a range of days. Sort by any column, also by integrity grade.",
      ],
      ["Global API", "The API numbers of every project, with p50, p95 and p99 latency."],
      ["Rules", "Saved traction rules, and the projects that meet them, with the evidence."],
    ],
  },
  {
    name: "Compare",
    pages: [
      ["Compare", "Up to five projects side by side, for web and API."],
      ["CSV export", "Every global and compare view exports exactly as shown."],
    ],
  },
  {
    name: "Admin",
    pages: [
      ["Settings", "Open enrollment, or approval before a project can start."],
      ["Operators", "The operator roster. Each operator confirms with a passkey."],
      ["API keys", "Keys that give other tools access to the metrics API."],
      ["AI / MCP", "Keys that let an AI client read the operator view."],
      ["Audit log", "Every recorded change, with the person who made it."],
    ],
  },
] as const;

export const integrationsIn = [
  {
    k: "Websites",
    via: "a script tag or npm",
    items: [
      ["HTML", "onboarding/web/static-html"],
      ["Next.js", "onboarding/web/nextjs"],
      ["Astro", "onboarding/web/astro"],
      ["SvelteKit", "onboarding/web/sveltekit"],
      ["Vue", "onboarding/web/vue"],
      ["Vite and React", "onboarding/web/vite-react"],
      ["React Router", "onboarding/web/react-router"],
      ["Docusaurus", "onboarding/web/docusaurus"],
      ["VitePress", "onboarding/web/vitepress"],
      ["Mintlify", "onboarding/web/mintlify"],
      ["npm", "onboarding/web/npm"],
    ],
  },
  {
    k: "APIs",
    via: "OpenTelemetry",
    items: [
      ["Node.js", "onboarding/api/node"],
      ["Python", "onboarding/api/python"],
      ["Go", "onboarding/api/go"],
      ["Rust", "onboarding/api/rust"],
      ["Java", "onboarding/api/java"],
      ["Stellar Anchor Platform", "onboarding/api/anchor-platform"],
      ["OpenTelemetry Collector", "onboarding/api/collector-fanout"],
    ],
  },
  {
    k: "Wallets",
    via: "wallet connect events",
    items: [["Stellar Wallets Kit", "onboarding/web/wallets-kit"]],
  },
] as const;

export const integrationsOut = [
  {
    k: "AI assistants",
    via: "MCP, read only",
    items: [
      ["Claude", "onboarding/mcp"],
      ["ChatGPT", "onboarding/mcp"],
      ["Cursor", "onboarding/mcp"],
    ],
  },
  {
    k: "Your team",
    via: "sign-in",
    items: [
      ["GitHub", "onboarding/sign-in"],
      ["Stellar wallet", "onboarding/sign-in"],
      ["Passkey", "onboarding/sign-in"],
    ],
  },
] as const;

// performance: docs/reference/gateway-limits.md, docs/reference/capacity.md,
// packages/tracker/releases/manifest.json, docs/reference/browser-proof.md
export const perf = [
  {
    n: "700",
    unit: "a second",
    k: "Web events ingested by one node",
    v: "The gateway of one production node accepts them into the queue at p99 50 ms. The expected peak is 50 a second.",
    wide: true,
  },
  {
    n: "1,000,000",
    unit: "requests",
    k: "API requests in one test",
    v: "Veristar stored all 1,000,000, equal to the requests sent, on 12 Nov 2026.",
    wide: true,
  },
  { n: "4,385", unit: "bytes", k: "Tracker size", v: "Version 1.3.1, after gzip.", wide: false },
  {
    n: "150",
    unit: "ms",
    k: "Browser proof",
    v: "The median solve stays under 150 ms, even with a 6x CPU slowdown.",
    wide: false,
  },
  {
    n: "72",
    unit: "hours",
    k: "Queue",
    v: "Accepted events wait up to 72 hours. Maintenance loses no data.",
    wide: false,
  },
];

export const ledger = [
  {
    k: "The code",
    v: "Apache 2.0. One repository holds the application, the infrastructure code, the tests and the runbooks.",
    href: hrefs.repo,
    aria: "Open the repository",
  },
  {
    k: "The releases",
    v: "Release images are signed with cosign. Each node checks the signature before it pulls an image.",
    href: hrefs.docs("security/supply-chain"),
    aria: "Read about the supply chain",
  },
  {
    k: "The tracker",
    v: "Each version is a fixed file. Your tag names its hash, and the browser refuses any other file.",
    href: hrefs.docs("security/verify-tracker"),
    aria: "Verify the tracker",
  },
  {
    k: "The engine",
    v: "An unmodified Umami 3.5.0 stores the web events, on the private network only.",
    href: hrefs.docs("adr/0002-overlay-not-fork"),
    aria: "Why Umami is not forked",
  },
  {
    k: "The support",
    v: "Questions and problems go to a public issue tracker.",
    href: hrefs.docs("support"),
    aria: "Read the support page",
  },
];

// service targets: docs/sla.md
export const targets = [
  { k: "Ingestion", v: "Your events reach Veristar", fig: "99.99", unit: "%" },
  { k: "Dashboards", v: "Your team and SCF read the numbers", fig: "99.90", unit: "%" },
  { k: "Security patches", v: "For a critical or high vulnerability", fig: "24", unit: " h" },
  { k: "Support answer", v: "In business days", fig: "5", unit: " days" },
  { k: "Support resolution", v: "In business days", fig: "10", unit: " days" },
];

export const drills = [
  "Node loss",
  "Database leader loss",
  "Point-in-time restore",
  "Rolling deploy",
];

// handover: docs/handover/plan.md
export const handover = [
  { k: "Repository", note: "" },
  { k: "Domain", note: "" },
  { k: "Cloud resources", note: "" },
  { k: "Data", note: "" },
  { k: "DNS", note: "" },
  { k: "Secrets", note: "every one rotated" },
];

export type Mark = "yes" | "no" | "mid";
export const versusCols = ["Google Analytics", "Plausible", "Umami on your server"];
export const versus: { q: string; a: [Mark, string][] }[] = [
  {
    q: "No cookies",
    a: [
      ["no", "Sets cookies"],
      ["yes", "Yes"],
      ["yes", "Yes"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "Free and hosted for you",
    a: [
      ["yes", "Yes"],
      ["mid", "Paid, or host it yourself"],
      ["no", "You run the server"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "Open source",
    a: [
      ["no", "No"],
      ["yes", "Yes"],
      ["yes", "Yes"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "API analytics from OpenTelemetry",
    a: [
      ["no", "No"],
      ["no", "No"],
      ["no", "No"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "On-chain activity next to web traffic",
    a: [
      ["no", "No"],
      ["no", "No"],
      ["no", "No"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "One method for every project",
    a: [
      ["no", "Each project sets its own"],
      ["no", "Each project sets its own"],
      ["no", "Each project sets its own"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "SCF can compare projects",
    a: [
      ["no", "No"],
      ["no", "No"],
      ["no", "No"],
      ["yes", "Yes"],
    ],
  },
  {
    q: "Signed reports anyone can verify",
    a: [
      ["no", "No"],
      ["no", "No"],
      ["no", "No"],
      ["yes", "Yes"],
    ],
  },
];

export const doors = [
  {
    for: "For project teams",
    name: "Builders",
    p: "Free analytics without cookies. A signed report for your next SCF application, instead of screenshots.",
    link: "Enroll your project",
    href: hrefs.signIn,
  },
  {
    for: "For the Stellar Community Fund",
    name: "SCF",
    p: "Compare every project on the same metrics. Review, approve and remove projects from one place.",
    link: "Operator guide",
    href: hrefs.docs("operators/overview"),
  },
  {
    for: "For anyone who checks",
    name: "Reviewers",
    p: "Open the link of a signed report. Check the numbers and the signature without an account.",
    link: "Signed reports",
    href: hrefs.docs("onboarding/signed-reports"),
  },
];

// faq: docs/compliance/jurisdictions.md, docs/compliance/privacy-strategy.md,
// docs/reference/browser-proof.md, docs/handover/plan.md
export const faq = [
  {
    q: "Do I need a consent banner?",
    a: "It depends on the country. In some countries, such as the United Kingdom, you can measure without consent if you meet the conditions. In others, such as Germany, use consent mode: the tracker sends nothing until your banner starts it. The jurisdictions page in the docs lists each country.",
  },
  {
    q: "Does Veristar store IP addresses?",
    a: "No. The gateway shortens the address to find the country and keeps it in memory only for the request. It is never logged, queued or stored.",
  },
  {
    q: "Can SCF see my visitors?",
    a: "No. SCF operators see totals for each project. They never see raw events or sessions, and they cannot open your dashboard.",
  },
  {
    q: "Will ad blockers stop the tracker?",
    a: "Each test run checks the tracker addresses against eight common filter lists, including EasyList, EasyPrivacy and the uBlock and AdGuard lists. On 12 Nov 2026, no list blocked the script or its requests.",
  },
  {
    q: "Can the tracker on my site change without me?",
    a: "No. Your tag names one version and its hash. The browser refuses any other file. To upgrade, you change the tag.",
  },
  {
    q: "How long is data kept?",
    a: "Hourly API totals are kept for at least 24 months, and daily totals as long as your project exists. Raw web events are kept for 25 months by default.",
  },
  {
    q: "What happens when a new operator takes over?",
    a: "The repository, the domain, the data and the DNS move to the new operator, and every secret is rotated. Your tag and your dashboards keep their addresses.",
  },
  {
    q: "What does Veristar cost?",
    a: "Nothing. Projects, SCF and everyone else pay no fees. There is no paid tier and there are no ads.",
  },
];
