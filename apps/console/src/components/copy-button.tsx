import { fade } from "@veristar/brand/motion";
import { Check, Copy } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";

type State = "idle" | "copied" | "failed";

// one copy button: the icon and the word "Copy" (the place says what it
// copies), or the icon alone in a tight header. `label` names the target for
// assistive technology ("Copy key"); `visibleLabel` replaces the word only
// where the place cannot say it (the details of an error)
export function CopyButton({
  text,
  label = "Copy",
  visibleLabel = "Copy",
  size = "sm",
  variant = "outline",
  iconOnly = false,
  accessibleLabel,
}: {
  text: string;
  label?: string;
  visibleLabel?: string;
  // a longer name for assistive technology when several copy buttons share
  // a page, starting with the visible label
  accessibleLabel?: string;
  size?: "sm" | "xs" | "default";
  variant?: "outline" | "ghost";
  iconOnly?: boolean;
}) {
  const [state, setState] = useState<State>("idle");
  useEffect(() => {
    if (state === "idle") return;
    const t = setTimeout(() => setState("idle"), 2000);
    return () => clearTimeout(t);
  }, [state]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setState("copied");
    } catch {
      // clipboard access can be refused (permissions, insecure context); say
      // so, the text stays selectable in the code block
      setState("failed");
    }
  }

  const copied = state === "copied";
  return (
    <>
      <Button
        type="button"
        variant={variant}
        size={iconOnly ? (size === "xs" ? "icon-xs" : "icon-sm") : size}
        onClick={copy}
        // an icon-only button keeps a name while it shows the check
        aria-label={
          iconOnly
            ? copied
              ? "Copied"
              : label
            : copied
              ? undefined
              : (accessibleLabel ?? (label !== visibleLabel ? label : undefined))
        }
        className={copied ? "text-success" : undefined}
      >
        <AnimatePresence mode="wait" initial={false}>
          <motion.span key={copied ? "y" : "n"} {...fade} className="inline-flex">
            {copied ? (
              <Check aria-hidden className="size-4" />
            ) : (
              <Copy aria-hidden className="size-4" />
            )}
          </motion.span>
        </AnimatePresence>
        {iconOnly ? null : copied ? "Copied" : visibleLabel}
      </Button>
      <span role="status" className="sr-only">
        {copied ? "Copied to the clipboard" : ""}
      </span>
      {state === "failed" ? (
        <span role="alert" className="text-caption text-critical">
          Copying was blocked. Select the text and copy it by hand.
        </span>
      ) : null}
    </>
  );
}
