import { Mark } from "@veristar/brand/mark";
import { fade, page, transition } from "@veristar/brand/motion";
import { Check, Minus, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useId, useState } from "react";
import { type Fate, paths } from "../content";
import { site } from "../links";
import { action } from "./ui";

type Path = keyof typeof paths;
const order: Path[] = ["web", "api"];

const fates: Record<Fate, { word: string; icon: typeof Check; tone: string }> = {
  kept: { word: "Kept", icon: Check, tone: "text-success" },
  dropped: { word: "Dropped", icon: X, tone: "text-fg-2" },
  never: { word: "Never set", icon: Minus, tone: "text-fg-2" },
};

// native radios, so arrow keys, one tab stop and the checked state come from
// the browser; the labels carry the look of the console's segmented control
function PathSwitch({ value, onChange }: { value: Path; onChange: (p: Path) => void }) {
  const name = useId();
  return (
    <fieldset className="flex gap-0.5 rounded-md border border-line p-0.5">
      <legend className="sr-only">Path</legend>
      {order.map((p) => {
        const on = p === value;
        return (
          <label
            key={p}
            className={`relative flex h-8 cursor-pointer items-center rounded-sm px-2.5 text-small transition-colors duration-(--vs-duration-press) ease-standard has-focus-visible:outline-2 has-focus-visible:outline-accent has-focus-visible:outline-offset-2 max-sm:h-(--vs-control-touch) ${on ? "text-fg" : "text-fg-2 hover:text-fg"}`}
          >
            <input
              type="radio"
              name={name}
              value={p}
              checked={on}
              onChange={() => onChange(p)}
              className="sr-only"
            />
            {on && (
              <motion.span
                layoutId="path-marker"
                transition={transition.layout}
                className="absolute inset-0 rounded-sm bg-selected"
                aria-hidden
              />
            )}
            <span className="relative">{paths[p].tab}</span>
          </label>
        );
      })}
    </fieldset>
  );
}

function OnePath() {
  const [path, setPath] = useState<Path>("web");
  const p = paths[path];
  return (
    <div className="min-w-0 overflow-hidden rounded-xl border border-line bg-raised">
      <div className="flex items-center justify-between gap-3 border-b border-line px-3 py-2 sm:px-4">
        <h2 className="text-small font-semibold" aria-live="polite">
          <span className="sm:hidden">One {p.noun}</span>
          <span className="max-sm:hidden">What happens to one {p.noun}</span>
        </h2>
        <PathSwitch value={path} onChange={setPath} />
      </div>
      <AnimatePresence mode="wait" initial={false}>
        <motion.div key={path} initial={fade.initial} animate={fade.animate} exit={fade.exit}>
          <dl>
            {p.fields.map((f) => {
              const fate = fates[f.fate];
              const Icon = fate.icon;
              const gone = f.fate !== "kept";
              return (
                <div
                  key={f.label}
                  className="grid grid-cols-[92px_minmax(0,1fr)_auto] items-center gap-2 border-b border-line-subtle px-3 py-2.5 text-small sm:grid-cols-[136px_minmax(0,1fr)_88px] sm:gap-3 sm:px-4"
                >
                  <dt className="text-fg-2">{f.label}</dt>
                  <dd
                    className={`min-w-0 break-words font-mono text-caption font-normal ${gone ? "text-fg-3 line-through" : "text-fg"}`}
                  >
                    {f.value}
                  </dd>
                  <dd className={`flex items-center justify-end gap-1 text-caption ${fate.tone}`}>
                    <Icon className="size-3" strokeWidth={2.6} aria-hidden />
                    {fate.word}
                  </dd>
                </div>
              );
            })}
          </dl>
          <p className="px-3 py-2.5 text-caption font-normal text-fg-3 sm:px-4">{p.foot}</p>
        </motion.div>
      </AnimatePresence>
    </div>
  );
}

export function Hero() {
  return (
    <motion.div
      initial={page.initial}
      animate={page.animate}
      className="grid items-center gap-10 pt-10 pb-8 sm:pt-20 sm:pb-16 lg:grid-cols-[minmax(0,1fr)_minmax(0,540px)] lg:gap-14"
    >
      <div className="flex flex-col gap-5 sm:gap-6">
        <span className="flex items-center gap-2 text-small text-fg-2">
          <Mark className="size-4 text-fg-2" />
          Shared analytics for the Stellar Community Fund
        </span>
        <h1 className="text-h1 tracking-tight sm:text-display">Usage numbers SCF can check.</h1>
        <p className="max-w-[500px] text-title font-normal text-fg-2">
          Free web and API analytics for projects building on Stellar, measured the same way for
          every project. No cookies, no stored IP addresses.
        </p>
        <div className="flex flex-col gap-2.5 sm:flex-row sm:flex-wrap">
          {site.signIn && (
            <a href={site.signIn} className={action.primary}>
              Enroll your project
            </a>
          )}
          <a href="#reviewers" className={action.outline}>
            How SCF reads the numbers
          </a>
        </div>
      </div>
      <OnePath />
    </motion.div>
  );
}
