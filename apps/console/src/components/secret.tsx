import { Eye, EyeOff, KeyRound } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { fade } from "@veristar/brand/motion";
import { type ReactNode, useState } from "react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";

// a secret shown once (R7): masked by default, reveal and copy, and the
// person confirms they stored it before the panel can close. afterwards
// only its prefix or last characters are ever shown.
export function OneTimeSecret({
  value,
  title,
  description,
  onDone,
  doneLabel = "Done",
  children,
}: {
  value: string;
  title: ReactNode;
  description?: ReactNode;
  onDone?: () => void;
  doneLabel?: string;
  children?: ReactNode;
}) {
  const [shown, setShown] = useState(false);
  const [stored, setStored] = useState(false);
  const masked = `${value.slice(0, Math.min(8, value.indexOf("_", 3) + 1 || 4))}${"•".repeat(24)}`;
  return (
    <div
      role="region"
      aria-label="New secret"
      className="flex animate-rise-in flex-col gap-3 rounded-lg border border-success-line bg-raised p-4"
    >
      <div className="flex items-start gap-3">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-line-strong text-success">
          <KeyRound aria-hidden className="size-4" />
        </span>
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="text-body font-semibold text-fg">{title}</span>
          {description ? <span className="text-small text-fg-2">{description}</span> : null}
        </div>
      </div>
      <div className="flex min-w-0 items-center gap-1 rounded-md border border-line-strong bg-sunken pl-3">
        <AnimatePresence mode="wait" initial={false}>
          <motion.code
            key={shown ? "v" : "m"}
            {...fade}
            className="min-w-0 flex-1 overflow-x-auto py-2 text-small whitespace-nowrap text-fg"
            aria-label={shown ? undefined : "Secret hidden"}
          >
            {shown ? value : masked}
          </motion.code>
        </AnimatePresence>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={shown ? "Hide the secret" : "Show the secret"}
          aria-pressed={shown}
          onClick={() => setShown((s) => !s)}
        >
          {shown ? <EyeOff aria-hidden /> : <Eye aria-hidden />}
        </Button>
        <div className="pr-1">
          <CopyButton text={value} size="xs" variant="ghost" />
        </div>
      </div>
      {children}
      {onDone ? (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <label className="flex items-center gap-2 text-small text-fg-2">
            <Checkbox checked={stored} onCheckedChange={(c) => setStored(c === true)} />I stored it
            somewhere safe. It is not shown again.
          </label>
          <Button type="button" variant="outline" size="sm" disabled={!stored} onClick={onDone}>
            {doneLabel}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
