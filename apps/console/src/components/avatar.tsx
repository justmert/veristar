import { cn } from "@veristar/brand/cn";

// the first letter or digit of a login or name, upper case: one letter, so
// two never read as a word ("NO") and a login and its mark always match
export function initial(name: string): string {
  return (
    name
      .replace(/[^0-9A-Za-z]/g, "")
      .slice(0, 1)
      .toUpperCase() || "?"
  );
}

// one mark for a person everywhere (sidebar, members, operators, audit): a
// round tile with the initial. `dashed` is a person who has not signed in
// yet (an open invite)
export function Avatar({
  name,
  dashed,
  className,
}: {
  name: string;
  dashed?: boolean;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-7 shrink-0 items-center justify-center rounded-full border text-caption font-semibold",
        dashed
          ? "border-dashed border-line-strong text-fg-2"
          : "border-line-strong bg-selected text-fg",
        className,
      )}
    >
      {initial(name)}
    </span>
  );
}
