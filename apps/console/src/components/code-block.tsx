import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";
import { CopyButton } from "@/components/copy-button";

// code a person copies: a header with its name and the copy action, the text
// in mono on the sunken surface, selectable as a whole
export function CodeBlock({
  code,
  label,
  copyLabel = "Copy",
  className,
  actions,
  wrap = false,
}: {
  code: string;
  label?: ReactNode;
  copyLabel?: string;
  className?: string;
  actions?: ReactNode;
  wrap?: boolean;
}) {
  return (
    <div className={cn("overflow-hidden rounded-lg border border-line bg-sunken", className)}>
      <div className="flex min-h-10 items-center justify-between gap-3 border-b border-line px-3 py-1">
        <span className="truncate text-caption text-fg-3">{label}</span>
        <div className="flex items-center gap-2">
          {actions}
          <CopyButton text={code} label={copyLabel} size="xs" variant="ghost" />
        </div>
      </div>
      <pre
        className={cn(
          "overflow-x-auto px-3 py-3 text-small text-fg",
          wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre",
        )}
      >
        <code>{code}</code>
      </pre>
    </div>
  );
}

// a short value on one line (a record name, a url, a key prefix) with copy
export function CopyField({
  value,
  label,
  className,
}: {
  value: string;
  label: string;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 items-center gap-1 rounded-md border border-line bg-sunken pl-2",
        className,
      )}
    >
      <code className="min-w-0 flex-1 truncate py-1 text-small text-fg">{value}</code>
      <CopyButton text={value} label={`Copy ${label}`} size="xs" variant="ghost" iconOnly />
    </div>
  );
}
