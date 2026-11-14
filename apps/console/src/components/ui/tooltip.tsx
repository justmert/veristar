import { cn } from "@veristar/brand/cn";
import { Tooltip as TooltipPrimitive } from "radix-ui";
import type * as React from "react";

// reveal motion from the trigger (css keyframes on radix's data-state, so
// radix waits for the exit animation before unmounting)
function TooltipProvider(props: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return <TooltipPrimitive.Provider delayDuration={300} skipDelayDuration={200} {...props} />;
}

function Tooltip({
  content,
  children,
  side = "top",
  className,
}: {
  content: React.ReactNode;
  children: React.ReactElement;
  side?: "top" | "right" | "bottom" | "left";
  className?: string;
}) {
  return (
    <TooltipPrimitive.Root>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          side={side}
          sideOffset={6}
          collisionPadding={8}
          className={cn(
            "z-50 max-w-72 rounded-md border border-line-strong bg-overlay px-2.5 py-1.5 text-caption font-normal text-fg shadow-overlay data-[state=closed]:animate-reveal-out data-[state=delayed-open]:animate-reveal-in data-[state=instant-open]:animate-fade-in",
            className,
          )}
        >
          {content}
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  );
}

export { Tooltip, TooltipProvider };
