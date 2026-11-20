import { cn } from "@veristar/brand/cn";
import { Check, CircleAlert, Info, Minus, X } from "lucide-react";
import type { ReactNode } from "react";

// the state of one check. passed is a quiet green check with plain text,
// never a pill; failed and warning carry the colour of a problem; missing
// is something not given (a dash), info a fact without a verdict
export type CheckStatus = "failed" | "warning" | "passed" | "missing" | "info";

export type CheckItem = {
  key: string;
  status: CheckStatus;
  // the check as one sentence
  children: ReactNode;
  // mono detail on its own line(s) under the sentence (a record, an ID)
  detail?: ReactNode;
  // at the right end: the action that fixes it, or a quiet date
  aside?: ReactNode;
};

const ICON: Record<CheckStatus, ReactNode> = {
  failed: <X aria-hidden strokeWidth={2.6} className="size-4 text-critical" />,
  warning: <CircleAlert aria-hidden strokeWidth={2.4} className="size-4 text-warning" />,
  passed: <Check aria-hidden strokeWidth={2.6} className="size-4 text-success" />,
  missing: <Minus aria-hidden strokeWidth={2.4} className="size-4 text-fg-3" />,
  info: <Info aria-hidden strokeWidth={2.2} className="size-4 text-fg-2" />,
};

const SAID: Record<CheckStatus, string> = {
  failed: "Failed",
  warning: "Needs action",
  passed: "Passed",
  missing: "Not given",
  info: "Note",
};

// one row per check: icon, sentence, optional mono detail under it,
// optional action or date at the right. rows are split by a thin line
// (Review checks, SCF ownership proofs)
export function ChecksList({
  items,
  label,
  className,
}: {
  items: CheckItem[];
  // the accessible name of the list ("Checks")
  label?: string;
  className?: string;
}) {
  return (
    <ul aria-label={label} className={cn("flex flex-col", className)}>
      {items.map((c) => (
        <CheckRow {...c} key={c.key} />
      ))}
    </ul>
  );
}

export function CheckRow({
  status,
  children,
  detail,
  aside,
}: Omit<CheckItem, "key"> & { key?: string }) {
  return (
    <li
      data-check={status}
      className="grid grid-cols-[22px_minmax(0,1fr)_auto] items-start gap-x-3 border-t border-line-subtle py-2.5 text-body leading-5.5 first:border-t-0"
    >
      <span className="pt-1">
        {ICON[status]}
        <span className="sr-only">{SAID[status]}: </span>
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="min-w-0 break-words text-fg">{children}</span>
        {detail ? (
          <span className="flex min-w-0 flex-col font-mono text-small leading-5 break-all text-fg-3">
            {detail}
          </span>
        ) : null}
      </span>
      {aside ? (
        <span className="flex items-center gap-2 self-center text-small text-fg-3">{aside}</span>
      ) : (
        <span />
      )}
    </li>
  );
}
