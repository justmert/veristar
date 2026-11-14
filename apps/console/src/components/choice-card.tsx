import { cn } from "@veristar/brand/cn";
import { type ReactNode, useId } from "react";

// one option of a radio group drawn as a card: a native radio (keyboard,
// forms and assistive technology work as usual), its title as the name and
// its description read with it. the whole card is the click target.
export function ChoiceCard({
  name,
  value,
  checked,
  onChange,
  title,
  description,
  badge,
  disabled,
  className,
}: {
  name: string;
  value: string;
  checked: boolean;
  onChange: () => void;
  title: string;
  description?: ReactNode;
  badge?: ReactNode;
  disabled?: boolean;
  className?: string;
}) {
  const id = useId();
  return (
    <label
      htmlFor={`${id}-r`}
      className={cn(
        "flex min-w-0 cursor-pointer flex-col gap-1 rounded-lg border border-line bg-raised p-3 transition-colors duration-(--vs-duration-reveal) ease-standard hover:border-line-strong has-checked:border-accent has-checked:bg-selected has-focus-visible:outline-2 has-focus-visible:outline-offset-2 has-focus-visible:outline-accent has-disabled:cursor-not-allowed has-disabled:opacity-50",
        className,
      )}
    >
      <span className="flex items-center gap-2 text-body font-medium text-fg">
        <span id={`${id}-t`}>{title}</span>
        {/* the native radio sits transparent over the drawn circle, so it
            stays the real, clickable and focusable control */}
        <span className="relative order-first flex size-3.5 shrink-0">
          <input
            id={`${id}-r`}
            type="radio"
            name={name}
            value={value}
            checked={checked}
            disabled={disabled}
            onChange={onChange}
            aria-labelledby={`${id}-t`}
            aria-describedby={description ? `${id}-d` : undefined}
            className="peer absolute inset-0 m-0 cursor-pointer opacity-0 disabled:cursor-not-allowed"
          />
          <span
            aria-hidden
            className="pointer-events-none flex size-3.5 items-center justify-center rounded-full border border-line-strong transition-colors duration-(--vs-duration-press) ease-standard peer-checked:border-accent peer-checked:[&>span]:bg-accent"
          >
            <span className="size-1.5 rounded-full bg-transparent" />
          </span>
        </span>
        {badge ? <span className="ml-auto">{badge}</span> : null}
      </span>
      {description ? (
        <span id={`${id}-d`} className="text-small font-normal text-fg-2">
          {description}
        </span>
      ) : null}
    </label>
  );
}
