import { cn } from "@veristar/brand/cn";
import { WrapText } from "lucide-react";
import { type ReactNode, useState } from "react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";

// code a person copies: a header with its name (never cut), the wrap
// toggle and an icon copy button, then the text in mono on the sunken
// surface. a long line scrolls sideways with a fade at the right edge and a
// visible scroll bar, or wraps when the reader turns wrapping on
export function CodeBlock({
  code,
  label,
  copyLabel = "Copy",
  copyText,
  className,
  actions,
  wrap: wrapDefault = false,
}: {
  code: string;
  label?: ReactNode;
  // the accessible name of the copy button: what it copies ("Copy command")
  copyLabel?: string;
  // what the copy action copies when the shown code is an example around it
  copyText?: string;
  className?: string;
  actions?: ReactNode;
  wrap?: boolean;
}) {
  const [wrap, setWrap] = useState(wrapDefault);
  return (
    <div className={cn("overflow-hidden rounded-lg border border-line bg-sunken", className)}>
      <div className="flex min-h-10 items-center justify-between gap-3 border-b border-line px-3 py-1">
        <span className="min-w-0 text-caption break-all text-fg-3">{label}</span>
        <div className="flex shrink-0 items-center gap-1">
          {actions}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-pressed={wrap}
            aria-label="Wrap lines"
            title="Wrap lines"
            onClick={() => setWrap((w) => !w)}
            className="max-lg:size-(--vs-control-touch)"
          >
            <WrapText aria-hidden />
          </Button>
          <CopyButton
            text={copyText ?? code}
            label={copyLabel}
            size="xs"
            variant="ghost"
            iconOnly
          />
        </div>
      </div>
      <pre
        className={cn(
          "px-3 py-3 text-small text-fg",
          wrap ? "whitespace-pre-wrap break-all" : "scroll-fade-x overflow-x-auto whitespace-pre",
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
