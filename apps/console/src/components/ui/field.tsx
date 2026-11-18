import { cn } from "@veristar/brand/cn";
import type * as React from "react";
import { Label } from "./label";

function Field({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="field"
      className={cn("group/field flex flex-col gap-1", className)}
      {...props}
    />
  );
}

function FieldLabel({ className, ...props }: React.ComponentProps<typeof Label>) {
  return (
    <Label className={cn("group-data-[invalid=true]/field:text-critical", className)} {...props} />
  );
}

function FieldDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="field-description"
      className={cn("text-caption font-normal text-fg-2", className)}
      {...props}
    />
  );
}

function FieldError({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      role="alert"
      data-slot="field-error"
      className={cn("animate-fade-in text-caption font-normal text-critical", className)}
      {...props}
    />
  );
}

export { Field, FieldDescription, FieldError, FieldLabel };
