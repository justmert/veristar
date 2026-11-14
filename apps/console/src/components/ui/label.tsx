import { cn } from "@veristar/brand/cn";
import { Label as LabelPrimitive } from "radix-ui";
import type * as React from "react";

function Label({ className, ...props }: React.ComponentProps<typeof LabelPrimitive.Root>) {
  return (
    <LabelPrimitive.Root
      data-slot="label"
      className={cn(
        "flex select-none items-center gap-2 text-small font-medium text-fg peer-disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { Label };
