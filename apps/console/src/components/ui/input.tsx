import { cn } from "@veristar/brand/cn";
import type * as React from "react";

export const controlClass =
  "w-full min-w-0 rounded-md border border-line-input bg-sunken px-2 text-body text-fg transition duration-(--vs-duration-reveal) ease-standard placeholder:text-fg-3 hover:border-fg-3 field-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-critical read-only:bg-raised";

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(controlClass, "h-(--vs-control) file:border-0 file:bg-transparent", className)}
      {...props}
    />
  );
}

export { Input };
