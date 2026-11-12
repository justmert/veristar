import { type ReactNode, useId } from "react";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
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
      {optional ? <span className="font-normal text-muted-foreground">(optional)</span> : null}
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
}: Common & Omit<React.ComponentProps<"select">, "id">) {
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

export function NativeSelect({ className, ...props }: React.ComponentProps<"select">) {
  return (
    <select
      className={`h-8 rounded-lg border border-input bg-card px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/40 aria-invalid:border-destructive disabled:opacity-50 ${className ?? ""}`}
      {...props}
    />
  );
}
