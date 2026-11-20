import { fade } from "@veristar/brand/motion";
import { Eye, EyeOff, KeyRound } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useId, useState } from "react";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";

// the one "copy it now" panel for every token and key: the secret is shown
// once, masked by default, with reveal and copy, and the person confirms
// they stored it before the panel can close. afterwards only its prefix or
// last characters are ever shown.
export function SecretPanel({
  value,
  noun,
  name,
  description,
  onDone,
  doneLabel = "Done",
  copyLabel = "Copy",
  plain = false,
  stored: storedProp,
  onStoredChange,
  children,
}: {
  value: string;
  // what the secret is ("key", "token"): the heading reads "Copy your new
  // key", with the name the person gave it as the line under it
  noun: string;
  name: string;
  description?: ReactNode;
  onDone?: () => void;
  doneLabel?: string;
  // names what is copied, such as "Copy key"
  copyLabel?: string;
  // inside a dialog that already names the secret: no box and no heading
  plain?: boolean;
  // the "I stored it" check, kept by the caller (a dialog that must not
  // close before it is ticked)
  stored?: boolean;
  onStoredChange?: (stored: boolean) => void;
  children?: ReactNode;
}) {
  const [shown, setShown] = useState(false);
  const [storedOwn, setStoredOwn] = useState(false);
  const stored = storedProp ?? storedOwn;
  const setStored = (v: boolean) => {
    setStoredOwn(v);
    onStoredChange?.(v);
  };
  const storedId = useId();
  const masked = `${value.slice(0, Math.min(8, value.indexOf("_", 3) + 1 || 4))}${"•".repeat(24)}`;
  return (
    <section
      aria-label="New secret"
      className={
        plain
          ? "flex flex-col gap-3"
          : "flex animate-rise-in flex-col gap-3 rounded-lg border border-line-2 bg-raised p-4"
      }
    >
      {plain ? null : (
        <div className="flex items-start gap-3">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-line-strong text-fg-2">
            <KeyRound aria-hidden className="size-4" />
          </span>
          <div className="flex min-w-0 flex-col gap-1">
            <span className="text-body font-semibold text-fg">Copy your new {noun}</span>
            <span className="text-small font-medium break-words text-fg">{name}</span>
            {description ? <span className="text-small text-fg-2">{description}</span> : null}
          </div>
        </div>
      )}
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
          <CopyButton text={value} label={copyLabel} size="xs" variant="ghost" />
        </div>
      </div>
      {children}
      {onDone || onStoredChange ? (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <span className="flex items-center gap-2">
            <Checkbox
              id={storedId}
              checked={stored}
              onCheckedChange={(c) => setStored(c === true)}
            />
            <label htmlFor={storedId} className="text-small text-fg-2">
              I stored it somewhere safe. It is not shown again.
            </label>
          </span>
          {onDone ? (
            <Button type="button" variant="outline" size="sm" disabled={!stored} onClick={onDone}>
              {doneLabel}
            </Button>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
