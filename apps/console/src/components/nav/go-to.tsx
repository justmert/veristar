import { cn } from "@veristar/brand/cn";
import { Search } from "lucide-react";
import { createContext, useContext } from "react";
import { Kbd } from "@/components/panel";
import { Button } from "@/components/ui/button";

// the signed-in shell owns the command palette; anything inside it can open
// the palette through this context. outside the shell (public pages) there
// is no palette and the value is null
export const PaletteContext = createContext<(() => void) | null>(null);

export function useOpenPalette(): (() => void) | null {
  return useContext(PaletteContext);
}

// "Go to" with its shortcut: the wide field-like button under the context
// switcher in the sidebar, or an outline button (the message pages). draws
// nothing where there is no palette
export function GoToButton({
  variant = "button",
  className,
}: {
  variant?: "sidebar" | "button";
  className?: string;
}) {
  const open = useOpenPalette();
  if (!open) return null;
  if (variant === "sidebar")
    return (
      <button
        type="button"
        onClick={open}
        aria-keyshortcuts="Control+K Meta+K"
        className={cn(
          "flex h-8 w-full items-center gap-2 rounded-md border border-line px-2.5 text-small text-fg-3 transition-colors duration-(--vs-duration-press) hover:border-line-strong hover:text-fg-2 focus-ring max-lg:h-(--vs-control-touch)",
          className,
        )}
      >
        <Search aria-hidden className="size-3.5 shrink-0" />
        Go to
        <span className="ml-auto">
          <Kbd>Ctrl K</Kbd>
        </span>
      </button>
    );
  return (
    <Button
      variant="outline"
      onClick={open}
      aria-keyshortcuts="Control+K Meta+K"
      className={cn("max-sm:h-(--vs-control-touch)", className)}
    >
      Go to
      <Kbd>Ctrl K</Kbd>
    </Button>
  );
}
