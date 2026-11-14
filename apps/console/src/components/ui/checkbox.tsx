import { cn } from "cn";
import { CheckIcon } from "lucide-react";
import { Checkbox as CheckboxPrimitive } from "radix-ui";
import type * as React from "react";

function Checkbox({ className, ...props }: React.ComponentProps<typeof CheckboxPrimitive.Root>) {
  return (
    <CheckboxPrimitive.Root
      data-slot="checkbox"
      className={cn(
        "peer relative flex size-4 shrink-0 items-center justify-center rounded-sm border border-line-strong bg-sunken text-on-accent transition duration-(--vs-duration-press) ease-standard after:absolute after:-inset-x-3 after:-inset-y-2 hover:border-fg-3 focus-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-critical data-[state=checked]:border-accent data-[state=checked]:bg-accent",
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator
        data-slot="checkbox-indicator"
        className="grid animate-fade-in place-content-center [&>svg]:size-3.5"
      >
        <CheckIcon strokeWidth={3} />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

export { Checkbox };
