import { Check, Copy } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";

type State = "idle" | "copied" | "failed";

export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
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

  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={copy}>
        {state === "copied" ? <Check aria-hidden /> : <Copy aria-hidden />}
        {state === "copied" ? "Copied" : label}
      </Button>
      <span role="status" className="sr-only">
        {state === "copied" ? "Copied to the clipboard" : ""}
      </span>
      {state === "failed" ? (
        <span role="alert" className="text-xs text-destructive">
          Copying was blocked. Select the text and copy it by hand.
        </span>
      ) : null}
    </>
  );
}
