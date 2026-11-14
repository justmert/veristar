import { ArrowUpRight, Code, Globe, Lock, ShieldCheck } from "lucide-react";
import { Footer } from "../components/footer";
import { Header } from "../components/header";
import { Hero } from "../components/hero";
import {
  action,
  Reveal,
  RevealItem,
  RevealList,
  Section,
  SectionHeading,
  textLink,
} from "../components/ui";
import {
  doors,
  handover,
  pipelines,
  privacy,
  privacyDocs,
  type Step,
  targets,
  trust,
} from "../content";
import { site } from "../links";

const trustIcons = [Globe, Code, ShieldCheck, Lock];

function TrustRow() {
  return (
    <RevealList as="ul" className="grid border-t border-line sm:grid-cols-2 lg:grid-cols-4">
      {trust.map((t, i) => {
        const Icon = trustIcons[i] ?? Globe;
        const href = site.docs(t.doc);
        const body = (
          <>
            <span className="flex items-center gap-2 text-body font-semibold sm:text-title">
              <Icon className="size-4 text-fg-2" aria-hidden />
              {t.title}
            </span>
            <span className="text-small text-fg-2">{t.detail}</span>
          </>
        );
        // phone: stacked rows; tablet: two by two; desktop: one row of four
        const cell = [
          "flex h-full flex-col gap-1 py-3.5 sm:px-5 sm:py-6",
          i > 0 ? "max-sm:border-t max-sm:border-line-subtle" : "",
          i % 2 === 1 ? "sm:border-l sm:border-line" : "sm:max-lg:pl-0",
          i >= 2 ? "sm:max-lg:border-t sm:max-lg:border-line" : "",
          i === 2 ? "lg:border-l lg:border-line" : "",
          i === 0 ? "lg:pl-0" : "",
        ].join(" ");
        return (
          <RevealItem key={t.title}>
            {href ? (
              <a
                href={href}
                className={`${cell} rounded-sm transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring`}
              >
                {body}
              </a>
            ) : (
              <div className={cell}>{body}</div>
            )}
          </RevealItem>
        );
      })}
    </RevealList>
  );
}

function Numbered({ steps }: { steps: Step[] }) {
  return (
    <RevealList className="flex flex-col">
      {steps.map((s, i) => (
        <RevealItem
          key={s.title}
          className="grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 border-t border-line-subtle py-3 text-body"
        >
          <span className="pt-0.5 font-mono text-caption font-normal text-fg-3">
            {String(i + 1).padStart(2, "0")}
          </span>
          <span>
            <span className="font-semibold">{s.title}</span>{" "}
            <span className="text-fg-2">{s.detail}</span>
          </span>
        </RevealItem>
      ))}
    </RevealList>
  );
}

function Doors() {
  const docsLink = site.docs("operators/reading-the-numbers");
  const cards = [
    { id: "teams", ...doors.teams, href: site.signIn, cta: "Enroll your project" },
    { id: "reviewers", ...doors.reviewers, href: docsLink, cta: "How SCF reads the numbers" },
  ];
  return (
    <Section id="reviewers" className="grid gap-6 py-12 sm:py-22 lg:grid-cols-2">
      {cards.map((c) => (
        <Reveal
          key={c.id}
          className="flex flex-col gap-4 sm:gap-5 sm:rounded-xl sm:border sm:border-line sm:bg-raised sm:p-7"
        >
          <span className="text-caption text-fg-3 uppercase">{c.who}</span>
          <h2 className="text-page tracking-tight">{c.title}</h2>
          <Numbered steps={c.steps} />
          {c.href && (
            <a href={c.href} className={`${textLink} self-start text-body font-medium`}>
              {c.cta}
            </a>
          )}
        </Reveal>
      ))}
    </Section>
  );
}

function HowItWorks() {
  return (
    <Section id="how" label="how-title" className="flex flex-col gap-8 py-12 sm:gap-10 sm:py-22">
      <Reveal>
        <SectionHeading id="how-title">How it works</SectionHeading>
      </Reveal>
      {pipelines.map((p) => (
        <Reveal key={p.title} className="flex flex-col gap-3.5">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h3 className="text-title">{p.title}</h3>
            {site.domain && (
              <span className="font-mono text-small text-fg-3">
                {p.host}.{site.domain}
              </span>
            )}
          </div>
          <RevealList className="grid overflow-hidden sm:grid-cols-2 sm:rounded-xl sm:border sm:border-line sm:bg-raised lg:grid-cols-4">
            {p.steps.map((s, i) => (
              <RevealItem
                key={s.title}
                className="grid grid-cols-[28px_minmax(0,1fr)] gap-2.5 border-t border-line-subtle py-3 sm:flex sm:flex-col sm:gap-2 sm:border-line sm:p-5 sm:nth-[-n+2]:border-t-0 sm:even:border-l lg:nth-[-n+4]:border-t-0 lg:border-l lg:first:border-l-0"
              >
                <span className="pt-0.5 font-mono text-caption font-normal text-fg-3">
                  {String(i + 1).padStart(2, "0")}
                </span>
                <span className="flex flex-col gap-1 sm:gap-2">
                  <span className="text-body font-semibold sm:text-title">{s.title}</span>
                  <span className="text-body text-fg-2">{s.detail}</span>
                </span>
              </RevealItem>
            ))}
          </RevealList>
        </Reveal>
      ))}
    </Section>
  );
}

function Privacy() {
  return (
    <Section
      id="privacy"
      label="privacy-title"
      className="grid gap-x-16 gap-y-6 py-12 sm:py-22 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
    >
      <Reveal className="flex flex-col gap-3.5">
        <SectionHeading id="privacy-title">Privacy, by how it is built</SectionHeading>
        {privacyDocs.map((d) => {
          const href = site.docs(d.doc);
          return href ? (
            <a key={d.doc} href={href} className={`${textLink} self-start text-body`}>
              {d.label}
            </a>
          ) : null;
        })}
      </Reveal>
      <RevealList as="dl" className="flex flex-col">
        {privacy.map((r) => (
          <RevealItem
            as="div"
            key={r.term}
            className="flex flex-col gap-1 border-t border-line py-3.5 sm:grid sm:grid-cols-[180px_minmax(0,1fr)] sm:gap-6 sm:py-4"
          >
            <dt className="text-body font-semibold">{r.term}</dt>
            <dd className="text-body text-fg-2">{r.detail}</dd>
          </RevealItem>
        ))}
      </RevealList>
    </Section>
  );
}

function Targets() {
  return (
    <Section label="targets-title" className="flex flex-col gap-5 py-12 sm:gap-7 sm:py-22">
      <Reveal className="flex items-baseline justify-between gap-4">
        <SectionHeading id="targets-title">Service targets</SectionHeading>
        {site.status && (
          <a
            href={site.status}
            className={`${textLink} inline-flex items-center gap-1 text-body max-sm:hidden`}
          >
            Live status
            <ArrowUpRight className="size-3.5" aria-hidden />
          </a>
        )}
      </Reveal>
      <RevealList
        as="div"
        className="grid grid-cols-2 rounded-xl border border-line bg-raised lg:grid-cols-4"
      >
        {targets.map((t, i) => (
          <RevealItem
            as="div"
            key={t.title}
            className={`flex flex-col gap-1.5 p-4 sm:p-6 ${i % 2 === 1 ? "border-l border-line" : ""} ${i >= 2 ? "max-lg:border-t max-lg:border-line" : ""} ${i === 2 ? "lg:border-l lg:border-line" : ""}`}
          >
            <span className="font-mono text-section font-medium tracking-tight tabular sm:text-metric">
              {t.value}
            </span>
            <span className="text-small font-medium sm:text-body">{t.title}</span>
            <span className="text-caption font-normal text-fg-2 sm:text-small">{t.detail}</span>
          </RevealItem>
        ))}
      </RevealList>
      {site.status && (
        <a
          href={site.status}
          className={`${textLink} inline-flex min-h-(--vs-control-touch) items-center gap-1 self-start text-body sm:hidden`}
        >
          Live status
          <ArrowUpRight className="size-3.5" aria-hidden />
        </a>
      )}
    </Section>
  );
}

// the real adoption state: no project has been named, and none is named
// before it gives written permission. change this only when a project has
// agreed in writing to be named.
function Adoption() {
  return (
    <Section
      label="adoption-title"
      className="grid items-center gap-x-16 gap-y-5 py-12 sm:py-18 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]"
    >
      <Reveal>
        <SectionHeading id="adoption-title">Projects on Veristar</SectionHeading>
      </Reveal>
      <Reveal className="flex flex-col gap-4 rounded-xl border border-dashed border-line-strong p-5 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between sm:p-7">
        <p className="max-w-[440px] text-body text-fg-2 sm:text-title sm:font-normal">
          No project is listed yet. A project appears here only after it enrolls and gives written
          permission to be named.
        </p>
        {site.signIn && (
          <a href={site.signIn} className={action.outline}>
            Enroll your project
          </a>
        )}
      </Reveal>
    </Section>
  );
}

function Handover() {
  return (
    <Section
      id="handover"
      label="handover-title"
      className="grid gap-x-10 gap-y-6 py-12 sm:py-22 lg:grid-cols-2"
    >
      <Reveal className="flex flex-col gap-4">
        <SectionHeading id="handover-title">Apache 2.0, built to be handed over</SectionHeading>
        <p className="text-body text-fg-2 sm:text-title sm:font-normal">
          Services, tracker, console, infrastructure as code, runbooks and compliance documents live
          in one repository under the Apache License 2.0. The tracker keeps Umami's MIT licence.
        </p>
      </Reveal>
      <RevealList className="flex flex-col">
        {handover.map((h) => (
          <RevealItem
            key={h.when}
            className="flex flex-col gap-1 border-t border-line py-3.5 sm:grid sm:grid-cols-[120px_minmax(0,1fr)] sm:gap-4"
          >
            <span className="font-mono text-small text-fg-3">{h.when}</span>
            <span className="text-body">{h.what}</span>
          </RevealItem>
        ))}
      </RevealList>
    </Section>
  );
}

export function HomePage() {
  return (
    <>
      <a
        href="#main"
        className="sr-only rounded-md bg-overlay px-3 py-2 text-body text-fg focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-20 focus-ring"
      >
        Skip to content
      </a>
      <Header />
      <main id="main">
        <section id="top" aria-label="Introduction" className="border-b border-line">
          <div className="mx-auto max-w-content px-4 sm:px-6">
            <Hero />
            <TrustRow />
          </div>
        </section>
        <Doors />
        <HowItWorks />
        <Privacy />
        <Targets />
        <Adoption />
        <Handover />
      </main>
      <Footer />
    </>
  );
}
