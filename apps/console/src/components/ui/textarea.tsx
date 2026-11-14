import { cn } from "cn";
import type * as React from "react";
import { controlClass } from "./input";

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(controlClass, "min-h-20 resize-y py-2", className)}
      {...props}
    />
  );
}

export { Textarea };
