import { arrival, page, stagger, transition } from "@veristar/brand/motion";
import { animate, MotionConfig, motion, useInView } from "motion/react";
import { type ReactNode, useEffect, useRef } from "react";

// people who ask for reduced motion keep opacity changes only
export function MotionProvider({ children }: { children: ReactNode }) {
  return (
    <MotionConfig reducedMotion="user" transition={transition.change}>
      {children}
    </MotionConfig>
  );
}

// a page settles in when its route changes (keyed by the caller)
export function PageMotion({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <motion.div initial={page.initial} animate={page.animate} className={className}>
      {children}
    </motion.div>
  );
}

// a list whose items rise in one after another the first time it shows
export function Stagger({
  children,
  className,
  as = "div",
  ...rest
}: {
  children: ReactNode;
  className?: string;
  as?: "div" | "ul" | "ol" | "tbody";
  // accessible names and data attributes pass through to the element
  "aria-label"?: string;
}) {
  const C = motion[as];
  return (
    <C initial="initial" animate="animate" variants={stagger} className={className} {...rest}>
      {children}
    </C>
  );
}

export function StaggerItem({
  children,
  className,
  as = "div",
  ...rest
}: {
  children: ReactNode;
  className?: string;
  as?: "div" | "li" | "tr";
  // accessible names and data attributes pass through to the element
  "aria-label"?: string;
  [data: `data-${string}`]: string | undefined;
}) {
  const C = motion[as];
  return (
    <C variants={{ initial: page.initial, animate: page.animate }} className={className} {...rest}>
      {children}
    </C>
  );
}

// something new: a row or an event that arrived while the page was open
export function Arrival({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <motion.div
      layout
      initial={arrival.initial}
      animate={arrival.animate}
      exit={arrival.exit}
      className={className}
    >
      {children}
    </motion.div>
  );
}

// a number that counts to its new value when it changes (never on the
// first render: a page load shows the value at once), and flashes once
export function AnimatedNumber({
  value,
  format,
  className,
}: {
  value: number;
  format: (n: number) => string;
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const prev = useRef(value);
  const fmt = useRef(format);
  fmt.current = format;
  useEffect(() => {
    const el = ref.current;
    const from = prev.current;
    prev.current = value;
    if (!el || from === value) return;
    el.classList.remove("animate-flash");
    void el.offsetWidth;
    el.classList.add("animate-flash");
    const controls = animate(from, value, {
      ...transition.change,
      onUpdate: (v) => {
        el.textContent = fmt.current(v);
      },
      onComplete: () => {
        el.textContent = fmt.current(value);
      },
    });
    return () => controls.stop();
  }, [value]);
  return (
    <span ref={ref} className={className}>
      {format(value)}
    </span>
  );
}

// draws a chart in once it scrolls into view
export function useRevealOnce<T extends Element>() {
  const ref = useRef<T>(null);
  const seen = useInView(ref, { once: true, amount: 0.2 });
  return { ref, seen };
}
