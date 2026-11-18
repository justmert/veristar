import { cn } from "@veristar/brand/cn";
import { CircleAlert, CircleCheck, CircleX, Info, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";

export type NoticeTone = "info" | "warning" | "critical" | "success";

const ICON: Record<NoticeTone, ReactNode> = {
  info: <Info aria-hidden />,
  warning: <CircleAlert aria-hidden />,
  critical: <CircleX aria-hidden />,
  success: <CircleCheck aria-hidden />,
};
const COLOUR: Record<NoticeTone, string> = {
  info: "text-fg-2",
  warning: "text-warning",
  critical: "text-critical",
  success: "text-success",
};

// one notice under the header of a page, only the one that applies: it names
// what is blocked and the next action
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
        "grid animate-rise-in grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-3 rounded-lg border bg-raised px-4 py-3 sm:grid-cols-[auto_minmax(0,1fr)_auto]",
        tone === "critical"
          ? "border-critical-line"
          : tone === "warning"
            ? "border-warning-line"
            : "border-line",
        className,
      )}
    >
      <span
        className={cn(
          "flex size-8 items-center justify-center self-start rounded-md border border-line-strong [&_svg]:size-4",
          COLOUR[tone],
        )}
      >
        {icon ?? ICON[tone]}
      </span>
      <div className="flex min-w-0 flex-col gap-1 text-body">
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

export { TriangleAlert as NoticeAlertIcon };
