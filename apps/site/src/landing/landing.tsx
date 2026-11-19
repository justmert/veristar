// the landing page, ported one to one from the approved design. the markup
// and class names match landing.css; the scripted motion lives in motion.ts.
// self-hosted fonts: fraunces with its soft and wonk axes, and figtree
import "@fontsource-variable/fraunces/full.css";
import "@fontsource-variable/fraunces/full-italic.css";
import "@fontsource-variable/figtree";
import "./tokens.css";
import "./landing.css";
import { type CSSProperties, Fragment, useEffect, useRef, useState } from "react";
import {
  doors,
  drills,
  faq,
  gateNames,
  handover,
  heroBars,
  integrationsIn,
  integrationsOut,
  ledger,
  opGroups,
  perf,
  reportLines,
  reviewSteps,
  strata,
  targets,
  travellers,
  verbs,
  versus,
  versusCols,
} from "./data";
import { hrefs } from "./hrefs";
import { startMotion } from "./motion";

// custom properties set from data; react writes them through the cssom, which
// a style-src 'self' policy allows
const vars = (v: Record<string, string | number>) => v as CSSProperties;

function Mark({ className = "mark" }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 32 32" aria-hidden="true">
      <circle className="ring" cx="16" cy="17" r="11" />
      <path className="ring" d="M16 6v22" />
      <path
        className="star"
        d="M16 1.5 L17.3 4.7 L20.5 6 L17.3 7.3 L16 10.5 L14.7 7.3 L11.5 6 L14.7 4.7 Z"
      />
    </svg>
  );
}

const Check = () => (
  <svg viewBox="0 0 24 24" aria-hidden="true">
    <path d="M5 12.5l4.5 4.5L19 7.5" />
  </svg>
);

function TLink({
  href,
  children,
  style,
}: {
  href: string;
  children: string;
  style?: CSSProperties;
}) {
  return (
    <a className="tlink" href={href} style={style}>
      {children}{" "}
      <span className="arrow" aria-hidden="true">
        →
      </span>
    </a>
  );
}

function SplitHead({ title, em, lead }: { title: string; em: string; lead?: string }) {
  return (
    <div className="split-head reveal">
      <div>
        <h2 className="h2">
          {title} <em>{em}</em>
        </h2>
      </div>
      {lead ? <p className="lead">{lead}</p> : null}
    </div>
  );
}

function Nav() {
  return (
    <header className="nav">
      <div className="wrap nav-in">
        <a className="brand" href="#top" aria-label="Veristar home">
          <Mark />
          Veristar
        </a>
        <nav className="nav-links" aria-label="Resources">
          <a href={hrefs.docsHome}>Docs</a>
          {hrefs.repo ? (
            <a className="opt" href={hrefs.repo}>
              GitHub
            </a>
          ) : null}
          <a className="opt" href={hrefs.status}>
            Status
          </a>
        </nav>
        <a className="btn btn-primary" href={hrefs.signIn}>
          Sign in
        </a>
      </div>
    </header>
  );
}

function Hero() {
  return (
    <section className="hero">
      <div className="wrap">
        <h1 className="display">
          <span className="ln">Usage you</span>{" "}
          <span className="ln">
            can <em>prove.</em>
          </span>
        </h1>
        <div className="hero-row">
          <p className="hero-sub enter d2">
            Web, API and on-chain analytics for every project on Stellar.{" "}
            <strong>One method for every project</strong>, so the Stellar Community Fund can trust
            and compare the numbers.
          </p>
          <div className="enter d3">
            <div className="cta">
              <a className="btn btn-primary" href={hrefs.signIn}>
                Enroll your project{" "}
                <span className="arrow" aria-hidden="true">
                  →
                </span>
              </a>
              <a className="btn btn-line" href={hrefs.docs("how-it-works")}>
                How it works
              </a>
            </div>
          </div>
        </div>
        <div
          className="hm enter d4"
          role="img"
          aria-label="Many projects, each measured its own way, shown as bars at different heights, angles and styles. A Veristar line sweeps across them, and every bar lands on one shared baseline in one style: one method for every project, so the numbers can be compared."
        >
          <div className="hm-stage" aria-hidden="true">
            <div className="hm-bars">
              {heroBars.map((b, k) => (
                <i
                  key={k}
                  className={`hm-bar ${b.v}`}
                  style={vars({
                    "--h": `${b.h}%`,
                    "--dy": `${b.dy}px`,
                    "--r": `${b.r}deg`,
                    "--k": k,
                  })}
                />
              ))}
            </div>
            <span className="hm-base" />
            <span className="hm-sweep" />
          </div>
          <p className="hm-say" aria-hidden="true">
            <span className="hm-a">Today, each project measures its own way.</span>
            <span className="hm-b">
              Veristar measures <em>every project the same way.</em>
            </span>
          </p>
        </div>
      </div>
    </section>
  );
}

function Problem() {
  const lap = (copy: number) =>
    reviewSteps.map((s) => (
      <Fragment key={`${copy}-${s.step}`}>
        <span className="mq-p">
          {s.step}
          <span className="mq-fix">{s.fix}</span>
        </span>
        <span className="mq-s" aria-hidden="true" />
      </Fragment>
    ));
  return (
    <section className="section inv problem">
      <div className="wrap">
        <h2 className="q-text reveal">
          Which projects on Stellar are <em>actually used?</em>
        </h2>
        <p className="mq-lead reveal">
          Today, each review of usage goes like this. Veristar fixes every step.
        </p>
      </div>
      <div
        className="mq"
        role="img"
        aria-label="Today the review repeats: a project assembles its own usage report, reviewers question the numbers, the project defends them, and the decision stays uncertain. Then the next report starts it again. Veristar fixes each step: it measures usage for the project, runs five checks on every page view, signs a report that anyone can verify, and uses the same metrics for every project."
      >
        <div className="mq-track" aria-hidden="true">
          <div className="mq-run">
            {lap(0)}
            {lap(1)}
            {lap(2)}
          </div>
        </div>
      </div>
      <div className="wrap">
        <div className="mq-after">
          <p className="mq-body reveal">
            SCF has no shared, trusted source of usage data for the projects it funds. Reviewers
            check eligibility for Growth Hack, the Public Goods Award and the Integration Track with
            numbers that each project reports itself.
          </p>
          <p className="mq-out reveal">
            Veristar measures every project with <em>the same method.</em> The circle stops.
          </p>
        </div>
      </div>
    </section>
  );
}

function HowItWorks() {
  return (
    <section className="section">
      <div className="wrap">
        <SplitHead title="You add. Veristar checks." em="Everyone reads." />
        <ol className="verbs stagger">
          {verbs.map((v) => (
            <li key={v.w} className="verb reveal">
              <span className="verb-who">{v.who}</span>
              <span className="verb-w">
                {v.w}
                <i>.</i>
              </span>
              <p>{v.p}</p>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

function Measure() {
  return (
    <section className="section measure">
      <div className="wrap">
        <SplitHead
          title="Web, API and on-chain"
          em="in one place."
          lead="Three sources, one page for each project. Each number has one definition, the same for every project."
        />
      </div>
      <div className="strata stagger">
        {strata.map((s) => (
          <div key={s.name} className="stratum reveal">
            <div className="stratum-head">
              <span className="stratum-name">{s.name}</span>
              <span className="stratum-from">{s.from}</span>
            </div>
            <dl className="terms">
              {s.terms.map((t) => (
                <div key={t.k} className="term-row">
                  <dt>{t.k}</dt>
                  <dd>{t.v}</dd>
                </div>
              ))}
            </dl>
          </div>
        ))}
      </div>
    </section>
  );
}

function Trust() {
  return (
    <section className="section" id="trust">
      <div className="wrap">
        <SplitHead
          title="Numbers you cannot"
          em="quietly inflate."
          lead="Each page view passes five checks. Follow four kinds of traffic through them."
        />
        <div
          className="course reveal"
          role="table"
          aria-label="Four kinds of traffic and the check where each one stops"
        >
          <div className="course-row course-head" role="row">
            <span className="col-label" role="columnheader">
              Traffic
            </span>
            <div className="gate-names" role="columnheader">
              {gateNames.map((k, i) => (
                <span key={k} className="gate-name">
                  <b>{i + 1}</b>
                  {k}
                </span>
              ))}
            </div>
            <span className="col-label" role="columnheader">
              Result
            </span>
          </div>
          {travellers.map((t) => (
            <div key={t.who} className={`course-row traveller ${t.fate}`} role="row">
              <span className="who" role="cell">
                {t.who}
              </span>
              <div
                className="lane-track"
                role="cell"
                aria-label={
                  t.at === 0
                    ? "Passes all five checks"
                    : `Stops at check ${t.at}, ${gateNames[t.at - 1]}`
                }
              >
                <span className="run" style={vars({ "--stop": t.stop })} />
                <span className="marker" style={vars({ "--stop": t.stop })} aria-hidden="true">
                  {t.fate === "ok" ? <Check /> : null}
                  {t.fate === "hold" ? (
                    <svg viewBox="0 0 24 24" aria-hidden="true">
                      <path d="M9 6v12" />
                      <path d="M15 6v12" />
                    </svg>
                  ) : null}
                  {t.fate === "no" ? (
                    <svg viewBox="0 0 24 24" aria-hidden="true">
                      <path d="M7 7l10 10" />
                      <path d="M17 7L7 17" />
                    </svg>
                  ) : null}
                </span>
              </div>
              <span className="verdict" role="cell">
                {t.verdict}
              </span>
            </div>
          ))}
        </div>
        <div className="course-foot">
          <p className="small measure-text">
            Held-back and rejected traffic is counted. Both are shown next to the real numbers.
          </p>
          <TLink href={hrefs.docs("security/bot-and-inflation")}>Bot and inflation defences</TLink>
        </div>

        <div className="proof-row">
          <div className="sheet-wrap reveal">
            <article className="sheet" aria-label="The parts of a signed usage report">
              <div className="sheet-top">
                <span className="brand brand-lead">
                  <Mark />
                  Veristar
                </span>
                <span className="caption">What a report contains</span>
              </div>
              <h3 className="sheet-title">Usage report</h3>
              <ol className="lines">
                {reportLines.map((r) => (
                  <li key={r.k} className="line">
                    <span className="line-k">{r.k}</span>
                    <span className="line-dots" aria-hidden="true" />
                    <span className="line-v">{r.v}</span>
                  </li>
                ))}
              </ol>
              <div className="sign">
                <b>Signed by Veristar</b>
                <span className="caption">
                  With its published Stellar key. A signed number never changes.
                </span>
              </div>
              <div className="seal" aria-hidden="true">
                <Mark />
                <span>Signed</span>
              </div>
            </article>
          </div>
          <div className="grades reveal">
            <div className="letters" aria-hidden="true">
              <span className="la">A</span>
              <span className="lb">B</span>
              <span className="lc">C</span>
            </div>
            <h3 className="h3">Each project gets an integrity grade, with the reasons.</h3>
            <p className="small">
              You see the same grade as SCF and can appeal it. The grade goes into every signed
              report. A reviewer opens the link and checks the signature without an account.
            </p>
            <TLink href={hrefs.docs("onboarding/signed-reports")}>How signed reports work</TLink>
          </div>
        </div>
      </div>
    </section>
  );
}

function Tok({
  kind,
  children,
  sup,
  bar,
}: {
  kind: "keep" | "change" | "drop";
  children: string;
  sup?: number;
  bar?: number;
}) {
  return (
    <span className={`tok ${kind}`}>
      {children}
      {sup ? <sup>{sup}</sup> : null}
      {bar !== undefined ? (
        <i className="bar" style={vars({ "--i": bar })} aria-hidden="true" />
      ) : null}
    </span>
  );
}

function Privacy() {
  const [view, setView] = useState<"arrives" | "stored">("stored");
  return (
    <section className="section sheet-bg" id="privacy">
      <div className="wrap">
        <div className="priv-head">
          <div className="reveal">
            <h2 className="h2">
              No cookies. No stored <em>IP addresses.</em>
            </h2>
          </div>
          <div className="switch reveal" role="group" aria-label="Show the data">
            <span className={view === "stored" ? "thumb right" : "thumb"} aria-hidden="true" />
            <button
              type="button"
              className={view === "arrives" ? "sw on" : "sw"}
              aria-pressed={view === "arrives"}
              onClick={() => setView("arrives")}
            >
              What arrives
            </button>
            <button
              type="button"
              className={view === "stored" ? "sw on" : "sw"}
              aria-pressed={view === "stored"}
              onClick={() => setView("stored")}
            >
              What is stored
            </button>
          </div>
        </div>
        <div className={view}>
          <div className="sent reveal">
            <span className="sent-src">Website</span>
            <div>
              <p>
                A page view arrives with its <Tok kind="keep">page path</Tok>, its{" "}
                <Tok kind="change" sup={1}>
                  referrer
                </Tok>
                , the <Tok kind="keep">device and browser</Tok>, the{" "}
                <Tok kind="drop" sup={2} bar={0}>
                  IP address
                </Tok>{" "}
                of the visitor and any{" "}
                <Tok kind="drop" bar={1}>
                  email addresses
                </Tok>{" "}
                or{" "}
                <Tok kind="drop" sup={3} bar={2}>
                  Stellar keys
                </Tok>{" "}
                in its data.
              </p>
              <ol className="notes">
                <li>
                  <b>1</b>
                  <span>Only the host of the site that sent the visitor is kept.</span>
                </li>
                <li>
                  <b>2</b>
                  <span>Shortened to find the country, then dropped. Never logged or stored.</span>
                </li>
                <li>
                  <b>3</b>
                  <span>Removed before the event goes into the queue.</span>
                </li>
              </ol>
            </div>
          </div>
          <div className="sent reveal">
            <span className="sent-src">API</span>
            <div>
              <p>
                A request arrives with its <Tok kind="keep">service name</Tok>, its{" "}
                <Tok kind="change" sup={4}>
                  route
                </Tok>
                , its <Tok kind="keep">status code</Tok>, its{" "}
                <Tok kind="change" sup={5}>
                  duration
                </Tok>
                , its{" "}
                <Tok kind="drop" sup={6} bar={0}>
                  trace and spans
                </Tok>{" "}
                and its{" "}
                <Tok kind="drop" bar={1}>
                  logs
                </Tok>
                .
              </p>
              <ol className="notes">
                <li>
                  <b>4</b>
                  <span>Variable parts become placeholders, as in /accounts/{"{id}"}.</span>
                </li>
                <li>
                  <b>5</b>
                  <span>Added to a latency histogram.</span>
                </li>
                <li>
                  <b>6</b>
                  <span>
                    Turned into request counts. Then the trace, the spans and the logs are dropped.
                  </span>
                </li>
              </ol>
            </div>
          </div>
        </div>
        <ul className="legend">
          <li>
            <span className="sw-k" />
            Kept
          </li>
          <li>
            <span className="sw-c" />
            Changed
          </li>
          <li>
            <span className="sw-d" />
            Dropped
          </li>
        </ul>
        <div className="pledges stagger">
          <div className="pledge reveal">
            <b>No cookies.</b>
            <span className="small">
              The tracker sets no cookie and writes no browser storage. A visitor is a token from a
              daily secret that is destroyed at 00:00 UTC.
            </span>
          </div>
          <div className="pledge reveal">
            <b>Kept in the EU.</b>
            <span className="small">
              Microsoft Azure, Denmark East. Erase one visitor on the day of the visit, one session
              or a whole site from the console.
            </span>
          </div>
          <div className="pledge reveal">
            <b>Visitors decide.</b>
            <span className="small">
              With Global Privacy Control, the tracker sends nothing. Where the law asks for
              consent, consent mode waits for your banner.
            </span>
          </div>
        </div>
        <span className="reveal after-7">
          <TLink href={hrefs.docs("compliance/privacy-strategy")}>Privacy strategy</TLink>
        </span>
      </div>
    </section>
  );
}

function Operators() {
  const [group, setGroup] = useState("Compare");
  const pages = opGroups.find((g) => g.name === group)?.pages ?? [];
  return (
    <section className="section ops" id="operators">
      <div className="wrap">
        <SplitHead
          title="One view across"
          em="every project."
          lead="Operators review projects, compare them and decide who can enroll. They see totals. They never see raw events or sessions."
        />
        <div className="index">
          <div className="words" role="group" aria-label="Parts of the operator area">
            {opGroups.map((g, i) => (
              <button
                key={g.name}
                type="button"
                className={g.name === group ? "word on" : "word"}
                aria-pressed={g.name === group}
                onClick={() => setGroup(g.name)}
              >
                <span className="word-n">0{i + 1}</span>
                {g.name}
              </button>
            ))}
          </div>
          <ul className="pages" aria-live="polite">
            {pages.map((p, i) => (
              <li key={`${group}-${p[0]}`} className="page" style={vars({ "--i": i })}>
                <b>{p[0]}</b>
                <span className="small">{p[1]}</span>
              </li>
            ))}
          </ul>
        </div>
        <div className="powers stagger">
          <div className="power reveal">
            <b>
              Cohorts and <em>percentiles</em>
            </b>
            <span className="small">
              See the position of a project in its SCF cohort for each metric.
            </span>
          </div>
          <div className="power reveal">
            <b>
              On-chain <em>activity</em>
            </b>
            <span className="small">
              Weekly active accounts, as the SCF Growth Hack rules define them. Only accounts and
              contracts that a project proves it controls count.
            </span>
          </div>
          <div className="power reveal">
            <b>
              Exports and <em>machine access</em>
            </b>
            <span className="small">
              Each global and compare view exports to CSV as shown. API keys give other tools the
              same totals.
            </span>
          </div>
        </div>
        <span className="reveal after-8">
          <TLink href={hrefs.docs("operators/overview")}>Operator guide</TLink>
        </span>
      </div>
    </section>
  );
}

type Group = {
  readonly k: string;
  readonly via: string;
  readonly items: readonly (readonly [string, string])[];
};
function Groups({ groups }: { groups: readonly Group[] }) {
  return (
    <>
      {groups.map((g) => (
        <div key={g.k} className="gg">
          <span className="gg-k">
            <b>{g.k}</b> · {g.via}
          </span>
          <ul>
            {g.items.map(([label, path]) => (
              <li key={label}>
                <a href={hrefs.docs(path)}>{label}</a>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </>
  );
}

function Integrations() {
  return (
    <section className="section galaxy">
      <div className="wrap">
        <SplitHead
          title="Works with"
          em="your stack."
          lead="Data comes in from your sites, APIs and wallets. Your team and AI assistants read it out. Each name opens its setup guide."
        />
        <div className="gx">
          <div className="gx-col gx-in">
            <span className="gx-side">In</span>
            <Groups groups={integrationsIn} />
          </div>
          <div className="gx-core" aria-hidden="true">
            <svg className="gx-star" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 0 L14.4 9.6 L24 12 L14.4 14.4 L12 24 L9.6 14.4 L0 12 L9.6 9.6 Z" />
            </svg>
            <span className="gx-name">Veristar</span>
          </div>
          <div className="gx-col gx-out">
            <span className="gx-side">Out</span>
            <Groups groups={integrationsOut} />
          </div>
        </div>
      </div>
    </section>
  );
}

function Performance({ armed }: { armed: boolean }) {
  return (
    <section className="section graph">
      <div className="wrap">
        <SplitHead
          title="Light for visitors."
          em="Ready for the ecosystem."
          lead="Every figure comes from a measured run, with its method in the docs."
        />
        <div className={armed ? "odos armed" : "odos"}>
          {perf.map((p) => (
            <div key={p.k} className={p.wide ? "odo-card reveal wide" : "odo-card reveal"}>
              <span
                className="odo-fig"
                role="img"
                aria-label={`${p.k === "Browser proof" ? "under " : ""}${p.n} ${p.unit}`}
              >
                <span className="odo-num">
                  {p.n.split("").map((ch, idx) => {
                    const digit = ch >= "0" && ch <= "9";
                    return digit ? (
                      <span key={idx} className="odo" aria-hidden="true">
                        <span className="strip" style={vars({ "--d": ch, "--k": idx * 2 + 1 })}>
                          {["0", "1", "2", "3", "4", "5", "6", "7", "8", "9"].map((n) => (
                            <i key={n}>{n}</i>
                          ))}
                        </span>
                      </span>
                    ) : (
                      <span key={idx} className="odo-sep" aria-hidden="true">
                        {ch}
                      </span>
                    );
                  })}
                </span>
                <span className="odo-unit">{p.unit}</span>
              </span>
              <span className="odo-k">{p.k}</span>
              <span className="small">{p.v}</span>
            </div>
          ))}
        </div>
        <span className="reveal after-8">
          <TLink href={hrefs.docs("reference/gateway-limits")}>Measured limits</TLink>
        </span>
      </div>
    </section>
  );
}

function OpenSource() {
  return (
    <section className="section">
      <div className="wrap">
        <SplitHead
          title="Read every line."
          em="Run it yourself."
          lead="The whole service is in one public repository, from the tracker to the infrastructure."
        />
        <ul className="ledger stagger">
          {ledger.map((l) => (
            <li key={l.k} className="reveal">
              <span className="ledger-what">{l.k}</span>
              <span className="small">{l.v}</span>
              {l.href ? (
                <a className="go" href={l.href}>
                  <span className="vh">{l.aria}</span>
                  <span aria-hidden="true">→</span>
                </a>
              ) : (
                <span className="go go-none" aria-hidden="true" />
              )}
            </li>
          ))}
        </ul>
        {hrefs.repo ? (
          <div className="cta reveal after-8">
            <a className="btn btn-line" href={hrefs.repo}>
              View on GitHub{" "}
              <span className="arrow" aria-hidden="true">
                →
              </span>
            </a>
          </div>
        ) : null}
      </div>
    </section>
  );
}

function Targets() {
  return (
    <section className="section tint">
      <div className="wrap">
        <SplitHead
          title="Promises,"
          em="measured from outside."
          lead="A probe in Azure East US, outside production, checks the service every 30 seconds. Anyone can see the results."
        />
        <div className="targets-grid">
          <div className="scores stagger" role="table" aria-label="Service targets">
            {targets.map((t) => (
              <div key={t.k} className="score reveal" role="row">
                <div className="score-what" role="cell">
                  <b>{t.k}</b>
                  <span className="small">{t.v}</span>
                </div>
                <span className="score-pct" role="cell">
                  {t.fig}
                  <i>{t.unit}</i>
                </span>
              </div>
            ))}
          </div>
          <div className="route-map reveal">
            <div className="site">
              <span className="site-k">The probe</span>
              <b>Azure East US</b>
              <span className="small">
                Outside production. It sends real events and reads the dashboards.
              </span>
            </div>
            <div className="route-line" aria-hidden="true">
              <span className="route-label">a check every 30 s</span>
            </div>
            <div className="site">
              <span className="site-k">Production</span>
              <b>Azure Denmark East</b>
              <ul className="zone-list">
                <li>Zone 1</li>
                <li>Zone 2</li>
                <li>Zone 3</li>
              </ul>
              <span className="small">Each zone runs ingestion and the core services.</span>
            </div>
            <div className="drills">
              <span className="site-k">Drilled on production, 13 and 14 Nov 2026</span>
              <ul className="drill-list">
                {drills.map((d) => (
                  <li key={d}>
                    <Check />
                    {d}
                  </li>
                ))}
              </ul>
            </div>
            <div className="route-links">
              <TLink href={hrefs.status}>Status page</TLink>
              <TLink href={hrefs.docs("sla")}>How each target is measured</TLink>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

function Handover() {
  return (
    <section className="section halves">
      <div className="wrap">
        <SplitHead
          title="Built to be"
          em="handed over."
          lead="Between month 6 and month 12, SCF can move the service to a community-appointed operator."
        />
        <div className="hand-cols">
          <span>Current operator</span>
          <span>Community operator</span>
        </div>
        <ol className="lanes">
          {handover.map((h) => (
            <li key={h.k} className="lane">
              <span className="item">
                {h.k}
                <i>{h.note}</i>
              </span>
            </li>
          ))}
        </ol>
        <div className="when stagger">
          <div className="reveal">
            <b>Month 0</b>
            <span className="small">
              The 12-month service window starts when SCF approves the final development tranche.
            </span>
          </div>
          <div className="reveal">
            <b>Month 6 to 12</b>
            <span className="small">
              SCF chooses the time. The new operator works beside the current one for a shadow
              period of at least four weeks, which SCF sets.
            </span>
          </div>
          <div className="reveal">
            <b>Done</b>
            <span className="small">
              A community-appointed operator runs the full system alone, from the repository.
            </span>
          </div>
        </div>
        <span className="reveal after-7">
          <TLink href={hrefs.docs("handover/plan")}>Handover plan</TLink>
        </span>
      </div>
    </section>
  );
}

function Comparison() {
  return (
    <section className="section">
      <div className="wrap">
        <SplitHead
          title="Why not"
          em="Google Analytics?"
          lead="General tools measure one site for its owner. Veristar measures every Stellar project in the same way, for its team and for SCF."
        />
        <div
          className="versus-wrap reveal"
          role="region"
          aria-label="Comparison table"
          // biome-ignore lint/a11y/noNoninteractiveTabindex: the table scrolls sideways on a phone, so the keyboard must reach it
          tabIndex={0}
        >
          <div
            className="versus"
            role="table"
            aria-label="Veristar compared with Google Analytics, Plausible and self-hosted Umami"
          >
            <div className="v-row v-head-row" role="row">
              <span className="v-head v-q" role="columnheader">
                <span className="vh">Question</span>
              </span>
              {versusCols.map((c) => (
                <span key={c} className="v-head" role="columnheader">
                  {c}
                </span>
              ))}
              <span className="v-head v-us" role="columnheader">
                <Mark />
                Veristar
              </span>
            </div>
            {versus.map((r, ri) => (
              <div key={r.q} className="v-row" role="row">
                <span className="v-q" role="rowheader">
                  {r.q}
                </span>
                {r.a.map(([kind, text], i) => (
                  <span
                    key={i}
                    className={`v-cell ${kind}${i === 3 ? " v-us" : ""}${i === 3 && ri === versus.length - 1 ? " v-last" : ""}`}
                    role="cell"
                  >
                    {text}
                  </span>
                ))}
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}

function Doors() {
  return (
    <section className="section tint">
      <div className="wrap">
        <div className="split-head reveal">
          <div>
            <h2 className="h2">
              For builders, for SCF, <em>for reviewers.</em>
            </h2>
          </div>
        </div>
        <div className="doors reveal">
          {doors.map((d) => (
            <div key={d.name} className="door">
              <span className="door-for">{d.for}</span>
              <div className="door-body">
                <p className="door-name">{d.name}</p>
                <p className="small">{d.p}</p>
                <TLink href={d.href}>{d.link}</TLink>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function Faq() {
  const [open, setOpen] = useState(0);
  return (
    <section className="section sheet-bg">
      <div className="wrap faq-grid">
        <div className="reveal">
          <h2 className="h2">
            Asked <em>often.</em>
          </h2>
          <p className="lead">Something else? Ask on the public issue tracker.</p>
          <span className="after-5">
            <TLink href={hrefs.docs("support")}>Support</TLink>
          </span>
        </div>
        <div className="faq">
          {faq.map((f, i) => (
            <div key={f.q} className={open === i ? "qa open" : "qa"}>
              <h3 className="qa-h">
                <button
                  type="button"
                  id={`faq-q-${i}`}
                  className="qa-q"
                  aria-expanded={open === i}
                  aria-controls={`faq-${i}`}
                  onClick={() => setOpen(open === i ? -1 : i)}
                >
                  {f.q}
                  <span className="plus" aria-hidden="true" />
                </button>
              </h3>
              <div className="qa-wrap" id={`faq-${i}`} role="region" aria-labelledby={`faq-q-${i}`}>
                <div className="qa-in">
                  <div className="qa-a">
                    <p>{f.a}</p>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function Closing() {
  return (
    <section className="block">
      <div className="wrap block-in">
        <h2 className="prove reveal">
          Prove <em>it.</em>
        </h2>
        <div className="reveal">
          <p>
            <strong>Measure your project the way SCF reads it.</strong> Share numbers that anyone
            can check, instead of numbers that you must defend.
          </p>
          <div className="cta">
            <a className="btn btn-primary" href={hrefs.signIn}>
              Enroll your project{" "}
              <span className="arrow" aria-hidden="true">
                →
              </span>
            </a>
            <a className="btn btn-line" href={hrefs.docsHome}>
              Read the docs
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}

function Footer() {
  return (
    <footer className="foot">
      <div className="wrap foot-in">
        <a className="brand" href="#top">
          <Mark />
          Veristar
        </a>
        <ul className="foot-links">
          <li>
            <a href={hrefs.console}>Console</a>
          </li>
          <li>
            <a href={hrefs.docsHome}>Docs</a>
          </li>
          <li>
            <a href={hrefs.status}>Status</a>
          </li>
          {hrefs.repo ? (
            <li>
              <a href={hrefs.repo}>GitHub</a>
            </li>
          ) : null}
          <li>
            <a href={hrefs.docs("support")}>Support</a>
          </li>
          <li>
            <a href={hrefs.docs("compliance/privacy-strategy")}>Privacy</a>
          </li>
        </ul>
      </div>
    </footer>
  );
}

export function Landing() {
  const root = useRef<HTMLDivElement>(null);
  // the performance figures start at zero and roll to their measured value once
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!root.current) return;
    return startMotion(root.current, { arm: () => setArmed(true), fire: () => setArmed(false) });
  }, []);
  return (
    <div className="vs" id="top" ref={root}>
      <a className="skip" href="#content">
        Skip to content
      </a>
      <Nav />
      <main id="content">
        <Hero />
        <Problem />
        <HowItWorks />
        <Measure />
        <Trust />
        <Privacy />
        <Operators />
        <Integrations />
        <Performance armed={armed} />
        <OpenSource />
        <Targets />
        <Handover />
        <Comparison />
        <Doors />
        <Faq />
        <Closing />
      </main>
      <Footer />
    </div>
  );
}
