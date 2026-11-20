import { cn } from "@veristar/brand/cn";
import { CircleAlert, CircleCheck, CircleX, Info } from "lucide-react";
import type { ReactNode } from "react";

export type NoticeTone = "info" | "warning" | "critical" | "success";

const ICON: Record<NoticeTone, ReactNode> = {
  info: <Info aria-hidden />,
  warning: <CircleAlert aria-hidden />,
  critical: <CircleX aria-hidden />,
  success: <CircleCheck aria-hidden />,
};
const COLOUR: Record<NoticeTone, string> = {
  info: "text-info",
  warning: "text-warning",
  critical: "text-critical",
  success: "text-success",
};

const LINE: Record<NoticeTone, string> = {
  info: "border-info-line",
  warning: "border-warning-line",
  critical: "border-critical-line",
  success: "border-success-line",
};

// one notice under the header of a page, only the one that applies: it names
// what is blocked and the next action. as the canvas band: a thin edge all
// round in the tone, the icon in its colour, the words neutral, no fill
export function Notice({
  tone,
  title,
  children,
  actions,
  icon,
  className,
  role = "status",
}: {
  tone: NoticeTone;
  title?: ReactNode;
  children?: ReactNode;
  actions?: ReactNode;
  icon?: ReactNode;
  className?: string;
  role?: "status" | "alert";
}) {
  return (
    <div
      role={role}
      className={cn(
        "grid animate-rise-in grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-2 rounded-xl border bg-transparent px-3.5 py-2.5 text-small sm:grid-cols-[auto_minmax(0,1fr)_auto]",
        LINE[tone],
        className,
      )}
    >
      <span className={cn("flex h-5 items-center self-start [&_svg]:size-4", COLOUR[tone])}>
        {icon ?? ICON[tone]}
      </span>
      <div className="flex min-w-0 flex-col gap-0.5 text-small">
        {title ? <span className="font-semibold text-fg">{title}</span> : null}
        {children ? <div className="text-fg-2">{children}</div> : null}
      </div>
      {actions ? (
        <div className="col-span-2 flex flex-wrap items-center gap-2 sm:col-span-1 sm:justify-end">
          {actions}
        </div>
      ) : null}
    </div>
  );
}
