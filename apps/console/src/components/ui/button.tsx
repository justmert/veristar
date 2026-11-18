import { cn } from "@veristar/brand/cn";
import { cva, type VariantProps } from "class-variance-authority";
import { LoaderCircle } from "lucide-react";
import { Slot } from "radix-ui";
import type * as React from "react";

// one accent button per page, for the page's main action (default);
// outline for the rest; ghost in toolbars and rows. a destructive action in
// a page, row or form is `destructive-outline`: crimson text and edge with
// an icon, so it never looks like the terracotta primary. the solid
// `destructive` fill is only the confirm button of a destructive dialog.
// navigation is a link, never a button. the press motion is a 1 px drop that confirms the
// click registered. a disabled button of any variant draws the disabled
// roles, so it never reads as a dim accent or as an enabled outline. a
// toggle passes aria-pressed and draws the pressed surface when on.
const DISABLED =
  "disabled:pointer-events-none disabled:border-disabled-line disabled:bg-disabled-fill disabled:text-disabled-fg disabled:decoration-disabled-line aria-disabled:pointer-events-none aria-disabled:border-disabled-line aria-disabled:bg-disabled-fill aria-disabled:text-disabled-fg";

const buttonVariants = cva(
  `group/button relative inline-flex shrink-0 select-none items-center justify-center gap-2 whitespace-nowrap rounded-md border border-transparent font-medium transition duration-(--vs-duration-press) ease-standard focus-ring active:not-aria-[haspopup]:translate-y-px aria-pressed:border-pressed-line aria-pressed:bg-pressed aria-pressed:text-fg ${DISABLED} [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4`,
  {
    variants: {
      variant: {
        default: "bg-accent text-on-accent hover:bg-accent-hover active:bg-accent-pressed",
        outline:
          "border-line-strong bg-transparent text-fg hover:bg-hover active:bg-selected aria-expanded:bg-selected",
        secondary: "bg-selected text-fg hover:bg-hover",
        ghost:
          "text-fg-2 hover:bg-hover hover:text-fg active:bg-selected aria-expanded:bg-selected aria-expanded:text-fg",
        destructive:
          "bg-critical-solid text-on-critical hover:bg-critical-solid-hover active:bg-critical-solid-hover",
        "destructive-outline":
          "border-critical-line bg-transparent text-critical hover:bg-critical-soft active:bg-critical-soft aria-expanded:bg-critical-soft",
        link: "h-auto border-0 px-0 text-fg underline decoration-line-strong underline-offset-4 hover:decoration-fg disabled:bg-transparent aria-disabled:bg-transparent",
      },
      size: {
        default: "h-(--vs-control) px-3 text-body",
        sm: "h-(--vs-control-sm) px-3 text-small",
        xs: "h-7 gap-1 rounded-sm px-2 text-caption [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-(--vs-control-touch) px-4 text-body",
        icon: "size-(--vs-control)",
        "icon-sm": "size-(--vs-control-sm)",
        "icon-xs": "size-7 rounded-sm [&_svg:not([class*='size-'])]:size-3.5",
        "icon-lg": "size-(--vs-control-touch)",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  pending = false,
  children,
  disabled,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
    // a request is running: the spinner replaces the leading icon and the
    // button stops taking clicks
    pending?: boolean;
  }) {
  const Comp = asChild ? Slot.Root : "button";
  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      aria-busy={pending || undefined}
      disabled={asChild ? undefined : disabled || pending}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    >
      {asChild ? (
        children
      ) : (
        <>
          {pending ? <LoaderCircle aria-hidden className="animate-spin" /> : null}
          {children}
        </>
      )}
    </Comp>
  );
}

export { Button, buttonVariants };
