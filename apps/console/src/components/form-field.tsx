import { cn } from "@veristar/brand/cn";
import { ChevronDown } from "lucide-react";
import { type ReactNode, useId } from "react";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { controlClass, Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";

type Common = {
  label: string;
  hint?: ReactNode;
  error?: string | null;
  optional?: boolean;
};

// label, control, hint and error wired together with ids, so a screen reader
// reads the hint and the error with the control
function useIds() {
  const base = useId();
  return { control: `${base}-c`, hint: `${base}-h`, error: `${base}-e` };
}

function describedBy(ids: ReturnType<typeof useIds>, hint: unknown, error: unknown) {
  const parts = [hint ? ids.hint : null, error ? ids.error : null].filter(Boolean);
  return parts.length ? parts.join(" ") : undefined;
}

function Label({ text, optional, htmlFor }: { text: string; optional?: boolean; htmlFor: string }) {
  return (
    <FieldLabel htmlFor={htmlFor}>
      {text}
      {optional ? <span className="font-normal text-fg-3">(optional)</span> : null}
    </FieldLabel>
  );
}

export function TextField({
  label,
  hint,
  error,
  optional,
  ...input
}: Common & Omit<React.ComponentProps<"input">, "id">) {
  const ids = useIds();
  return (
    <Field data-invalid={error ? true : undefined}>
      <Label text={label} optional={optional} htmlFor={ids.control} />
      <Input
        id={ids.control}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(ids, hint, error)}
        {...input}
      />
      {hint ? <FieldDescription id={ids.hint}>{hint}</FieldDescription> : null}
      {error ? <FieldError id={ids.error}>{error}</FieldError> : null}
    </Field>
  );
}

export function TextAreaField({
  label,
  hint,
  error,
  optional,
  ...input
}: Common & Omit<React.ComponentProps<"textarea">, "id">) {
  const ids = useIds();
  return (
    <Field data-invalid={error ? true : undefined}>
      <Label text={label} optional={optional} htmlFor={ids.control} />
      <Textarea
        id={ids.control}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(ids, hint, error)}
        {...input}
      />
      {hint ? <FieldDescription id={ids.hint}>{hint}</FieldDescription> : null}
      {error ? <FieldError id={ids.error}>{error}</FieldError> : null}
    </Field>
  );
}

// a native select: it works with every assistive technology and on phones
// without a custom popup
export function SelectField({
  label,
  hint,
  error,
  children,
  ...select
}: Common & Omit<React.ComponentProps<"select">, "id" | "size">) {
  const ids = useIds();
  return (
    <Field data-invalid={error ? true : undefined}>
      <Label text={label} htmlFor={ids.control} />
      <NativeSelect
        id={ids.control}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(ids, hint, error)}
        {...select}
      >
        {children}
      </NativeSelect>
      {hint ? <FieldDescription id={ids.hint}>{hint}</FieldDescription> : null}
      {error ? <FieldError id={ids.error}>{error}</FieldError> : null}
    </Field>
  );
}

// a native select drawn like the other controls, with its own chevron.
// `bare` drops the border and fill, for a select that sits inside a
// labelled box of its own (the filter bars).
export function NativeSelect({
  className,
  size = "md",
  bare = false,
  ...props
}: Omit<React.ComponentProps<"select">, "size"> & { size?: "sm" | "md"; bare?: boolean }) {
  return (
    <span className={`relative inline-flex min-w-0 ${className ?? ""}`}>
      <select
        className={cn(
          controlClass,
          // a select matches :read-only, which the shared control style
          // gives a raised fill meant for read-only inputs
          "w-full appearance-none pr-8 read-only:bg-sunken",
          size === "sm"
            ? "h-(--vs-control-sm) text-small max-lg:h-(--vs-control-touch)"
            : "h-(--vs-control)",
          bare &&
            "h-full border-0 bg-transparent pl-1.5 hover:border-0 focus:outline-none focus-visible:outline-none",
        )}
        {...props}
      />
      <ChevronDown
        aria-hidden
        className="pointer-events-none absolute top-1/2 right-2.5 size-4 -translate-y-1/2 text-fg-3"
      />
    </span>
  );
}
