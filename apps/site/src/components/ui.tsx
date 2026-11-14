import { page, stagger } from "@veristar/brand/motion";
import { motion } from "motion/react";
import type { ReactNode } from "react";

// the console's button language, as links: one accent action per screen,
// outline for the rest. 44 px on every screen, the site is mostly read on
// phones and touch targets matter there.
const base =
  "inline-flex h-(--vs-control-touch) shrink-0 select-none items-center justify-center gap-2 whitespace-nowrap rounded-md border px-4 text-body font-medium transition duration-(--vs-duration-press) ease-standard focus-ring active:translate-y-px";

export const action = {
  primary: `${base} border-transparent bg-accent text-on-accent hover:bg-accent-hover`,
  outline: `${base} border-line-strong text-fg hover:bg-hover active:bg-selected`,
};

export const textLink =
  "rounded-sm text-fg underline decoration-line-strong underline-offset-4 transition-colors duration-(--vs-duration-press) ease-standard hover:decoration-fg focus-ring";

// a section rises in once, the first time it scrolls into view
const viewport = { once: true, margin: "0px 0px -12% 0px" } as const;

export function Reveal({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <motion.div
      initial={page.initial}
      whileInView={page.animate}
      viewport={viewport}
      className={className}
    >
      {children}
    </motion.div>
  );
}

// a list whose items rise in one after another, once
export function RevealList({
  children,
  className,
  as = "ol",
}: {
  children: ReactNode;
  className?: string;
  as?: "ol" | "ul" | "div" | "dl";
}) {
  const C = motion[as];
  return (
    <C
      initial="initial"
      whileInView="animate"
      viewport={viewport}
      variants={stagger}
      className={className}
    >
      {children}
    </C>
  );
}

const item = { initial: page.initial, animate: page.animate };

export function RevealItem({
  children,
  className,
  as = "li",
}: {
  children: ReactNode;
  className?: string;
  as?: "li" | "div";
}) {
  const C = motion[as];
  return (
    <C variants={item} className={className}>
      {children}
    </C>
  );
}

export function Section({
  id,
  label,
  children,
  className = "",
}: {
  id?: string;
  label?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section id={id} aria-labelledby={label} className="border-b border-line">
      <div className={`mx-auto max-w-content px-4 sm:px-6 ${className}`}>{children}</div>
    </section>
  );
}

export function SectionHeading({ id, children }: { id: string; children: ReactNode }) {
  return (
    <h2 id={id} className="text-page sm:text-h1 tracking-tight">
      {children}
    </h2>
  );
}
