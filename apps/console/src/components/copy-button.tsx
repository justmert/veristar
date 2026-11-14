import { Check, Copy } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { fade } from "@veristar/brand/motion";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";

type State = "idle" | "copied" | "failed";

export function CopyButton({
  text,
  label = "Copy",
  size = "sm",
  variant = "outline",
  iconOnly = false,
}: {
  text: string;
  label?: string;
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
        aria-label={iconOnly ? label : undefined}
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
        {iconOnly ? null : copied ? "Copied" : label}
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
