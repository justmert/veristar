import { cn } from "@veristar/brand/cn";
import { Check } from "lucide-react";
import type { ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { formatCount } from "@/lib/format";

// "Where events went" (Web) and "Status codes" (API): the main share in
// large type with the other groups beside it, the full bar, and the thin
// slice that is not the main group opened into a second bar of every part
// at its true size, each named with its count. with nothing outside the
// main group, one quiet line says so.

export type ZoomTone = "success" | "warning" | "critical" | "neutral" | "muted";

export type ZoomGroup = {
  label: string;
  tone: ZoomTone;
  // the definition, on hover and for assistive technology
  definition?: string;
};

export type ZoomPart = {
  key: string;
  label: string;
  count: number;
  // the group this part belongs to (its label), for the side figures
  group: string;
  // the parts of one group step down in shade: 0, 1, 2, 3
  shade?: number;
  definition?: string;
};

const FILL: Record<ZoomTone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  neutral: "bg-fg-3",
  muted: "bg-line-strong",
};
const SHADE = ["opacity-100", "opacity-80", "opacity-65", "opacity-50"];

const pct = (v: number, total: number) =>
  total ? `${Math.round((v / total) * 1000) / 10}%` : "0%";

export function ZoomBlock({
  title,
  unit,
  main,
  groups,
  parts,
  caption,
  cleanText,
  footer,
  className,
}: {
  title: string;
  // the word after the total ("events", "requests")
  unit: string;
  // the main group: its share is the large figure ("Counted", "Succeeded")
  main: { label: string; count: number; definition?: string };
  // the groups beside it, in order ("Quarantined", "Bots", "Rejected")
  groups: ZoomGroup[];
  // every part outside the main group, in order
  parts: ZoomPart[];
  // under the second bar ("Not counted, by reason", "Errors, by code")
  caption: string;
  // the line shown when every item is in the main group
  cleanText: string;
  // a quiet last line inside the panel, such as the success codes of the api
  footer?: ReactNode;
  className?: string;
}) {
  const rest = parts.reduce((a, p) => a + p.count, 0);
  const total = main.count + rest;
  const groupOf = (label: string) => groups.find((g) => g.label === label);
  const sums = groups.map((g) => ({
    ...g,
    count: parts.filter((p) => p.group === g.label).reduce((a, p) => a + p.count, 0),
  }));
  const live = parts.filter((p) => p.count > 0);
  const mainShare = total ? (main.count / total) * 100 : 100;
  return (
    <section
      aria-label={title}
      className={cn(
        "flex min-w-0 flex-col gap-3.5 rounded-2xl border border-line bg-raised px-5 py-4",
        className,
      )}
    >
      <div className="flex items-baseline gap-2.5">
        <h2 className="text-body font-semibold text-fg">{title}</h2>
        <span className="text-[12.5px] text-fg-3 tabular">
          {formatCount(total)} {unit}
        </span>
      </div>

      <dl className="flex flex-wrap items-baseline gap-x-7 gap-y-3">
        <Figure label={main.label} definition={main.definition}>
          <span className="text-[26px] leading-[30px] font-semibold tabular">
            {pct(main.count, total)}
          </span>
        </Figure>
        {sums.map((g) => (
          <Figure key={g.label} label={g.label} definition={g.definition} tone={g.tone}>
            <span className="text-[20px] leading-6 font-semibold tabular">
              {formatCount(g.count)}
            </span>
            <span className="text-small font-normal text-fg-3 tabular">{pct(g.count, total)}</span>
          </Figure>
        ))}
      </dl>

      {rest > 0 ? (
        <>
          <div aria-hidden className="flex flex-col">
            <div className="flex h-3 gap-0.5 overflow-hidden rounded-[7px]">
              <i className="block h-full bg-success opacity-60" style={{ flexGrow: main.count }} />
              {sums
                .filter((g) => g.count > 0)
                .map((g) => (
                  <i
                    key={g.label}
                    className={cn("block h-full min-w-1", FILL[g.tone])}
                    style={{ flexGrow: g.count }}
                  />
                ))}
            </div>
            <svg
              width="100%"
              height="30"
              viewBox="0 0 100 30"
              preserveAspectRatio="none"
              aria-hidden="true"
              className="block text-line-strong"
            >
              <polygon
                points={`${mainShare.toFixed(2)},0 100,0 100,30 0,30`}
                className="fill-fg opacity-5"
              />
              <polyline
                points={`${mainShare.toFixed(2)},0 0,30`}
                fill="none"
                stroke="currentColor"
                strokeWidth="1"
                vectorEffect="non-scaling-stroke"
              />
              <polyline
                points="100,0 100,30"
                fill="none"
                stroke="currentColor"
                strokeWidth="1"
                vectorEffect="non-scaling-stroke"
              />
            </svg>
            <div className="flex h-[22px] gap-0.5 overflow-hidden rounded-lg">
              {live.map((p) => (
                <i
                  key={p.key}
                  title={`${p.label}: ${formatCount(p.count)}`}
                  className={cn(
                    "block h-full",
                    FILL[groupOf(p.group)?.tone ?? "neutral"],
                    SHADE[(p.shade ?? 0) % SHADE.length],
                  )}
                  style={{ flexGrow: p.count }}
                />
              ))}
            </div>
            <span className="mt-2 text-caption font-normal text-fg-3">{caption}</span>
          </div>
          <ul
            aria-label={caption}
            className="grid gap-2"
            style={{
              gridTemplateColumns: `repeat(auto-fit, minmax(min(9rem, 100%), 1fr))`,
            }}
          >
            {live.map((p) => {
              const body = (
                <li
                  key={p.key}
                  data-key={p.key}
                  aria-label={
                    p.definition
                      ? `${p.label}, ${formatCount(p.count)}. ${p.definition}`
                      : undefined
                  }
                  className="flex min-w-0 flex-col gap-[3px] rounded-[10px] border border-line px-3 py-2.5"
                >
                  <span className="flex min-w-0 items-center gap-[7px] text-small">
                    <i
                      aria-hidden
                      className={cn(
                        "block size-2.5 shrink-0 rounded-[3px]",
                        FILL[groupOf(p.group)?.tone ?? "neutral"],
                        SHADE[(p.shade ?? 0) % SHADE.length],
                      )}
                    />
                    <span className="truncate">{p.label}</span>
                  </span>
                  <span className="text-title font-semibold tabular">{formatCount(p.count)}</span>
                </li>
              );
              return p.definition ? (
                <Tooltip key={p.key} content={p.definition}>
                  {body}
                </Tooltip>
              ) : (
                body
              );
            })}
          </ul>
        </>
      ) : (
        <span className="flex items-center gap-2 text-small text-fg-2">
          <Check aria-hidden strokeWidth={2.6} className="size-3.5 text-success" />
          {cleanText}
        </span>
      )}
      {footer ? <div className="text-small text-fg-3">{footer}</div> : null}
    </section>
  );
}

function Figure({
  label,
  definition,
  tone,
  children,
}: {
  label: string;
  definition?: string;
  tone?: ZoomTone;
  children: ReactNode;
}) {
  const name = (
    <span className="underline decoration-line-strong decoration-dotted underline-offset-[3px]">
      {label}
    </span>
  );
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="flex items-center gap-1.5 text-[12.5px] text-fg-2">
        {tone ? <i aria-hidden className={cn("block size-2 rounded-full", FILL[tone])} /> : null}
        {definition ? (
          <Tooltip content={definition}>
            {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on focus too */}
            <span tabIndex={0} className="rounded-sm focus-ring">
              {name}
              <span className="sr-only">. {definition}</span>
            </span>
          </Tooltip>
        ) : (
          name
        )}
      </dt>
      <dd className="flex items-baseline gap-0.5 text-fg">{children}</dd>
    </div>
  );
}
