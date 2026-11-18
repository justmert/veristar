// motion presets for the `motion` library, built from tokens.ts. components
// use these names, never a literal duration or easing, so the feel of the
// whole product changes here. MotionConfig reducedMotion="user" in each app
// turns every transform off for people who ask for reduced motion and keeps
// the opacity changes.

import { motion as m } from "./tokens";

const s = (ms: number) => ms / 1000;
const ease = m.ease.standard as unknown as [number, number, number, number];
const easeExit = m.ease.exit as unknown as [number, number, number, number];

export const transition = {
  press: { duration: s(m.duration.press), ease },
  reveal: { duration: s(m.duration.reveal), ease },
  change: { duration: s(m.duration.change), ease },
  arrival: { duration: s(m.duration.arrival), ease },
  page: { duration: s(m.duration.page), ease },
  // a value counting to its new number
  number: { duration: s(m.duration.number), ease },
  exit: { duration: s(m.duration.press), ease: easeExit },
  // a spring for layout moves (the current nav item, a segmented marker)
  layout: { type: "spring", ...m.spring },
} as const;

const d = m.distance;

// menus, tooltips and popovers rise from their trigger
export const reveal = {
  initial: { opacity: 0, y: d, scale: 0.98 },
  animate: { opacity: 1, y: 0, scale: 1, transition: transition.reveal },
  exit: { opacity: 0, y: d / 2, scale: 0.98, transition: transition.exit },
} as const;

// a dialog grows slightly into place over its scrim
export const dialog = {
  initial: { opacity: 0, scale: 0.97, y: d * 2 },
  animate: { opacity: 1, scale: 1, y: 0, transition: transition.change },
  exit: { opacity: 0, scale: 0.98, transition: transition.exit },
} as const;

// a new row or event slides in from above
export const arrival = {
  initial: { opacity: 0, y: -d },
  animate: { opacity: 1, y: 0, transition: transition.arrival },
  exit: { opacity: 0, transition: transition.exit },
} as const;

// a page or a tab panel settles in
export const page = {
  initial: { opacity: 0, y: d },
  animate: { opacity: 1, y: 0, transition: transition.page },
  exit: { opacity: 0, transition: transition.exit },
} as const;

// lists reveal their children one after another, briefly
export const stagger = {
  animate: {
    transition: { staggerChildren: s(m.stagger.children), delayChildren: s(m.stagger.delay) },
  },
} as const;

export const fade = {
  initial: { opacity: 0 },
  animate: { opacity: 1, transition: transition.reveal },
  exit: { opacity: 0, transition: transition.exit },
} as const;

// the 1 px press every button shares
export const press = { scale: 0.98, y: 1, transition: transition.press } as const;

export const durationMs = m.duration;
