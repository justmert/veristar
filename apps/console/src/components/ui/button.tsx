import { cn } from "@veristar/brand/cn";
import { cva, type VariantProps } from "class-variance-authority";
import { LoaderCircle } from "lucide-react";
import { Slot } from "radix-ui";
import type * as React from "react";

// one accent button per screen (default); outline for the rest; ghost in
// toolbars and rows; destructive is the solid red with dark text. the press
// motion is a 1 px drop that confirms the click registered.
const buttonVariants = cva(
  "group/button relative inline-flex shrink-0 select-none items-center justify-center gap-2 whitespace-nowrap rounded-md border border-transparent font-medium transition duration-(--vs-duration-press) ease-standard focus-ring active:not-aria-[haspopup]:translate-y-px disabled:pointer-events-none disabled:opacity-50 aria-disabled:pointer-events-none aria-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default: "bg-accent text-on-accent hover:bg-accent-hover active:bg-accent-pressed",
        outline:
          "border-line-strong bg-transparent text-fg hover:bg-hover active:bg-selected aria-expanded:bg-selected",
        secondary: "bg-selected text-fg hover:bg-hover",
        ghost:
          "text-fg-2 hover:bg-hover hover:text-fg active:bg-selected aria-expanded:bg-selected aria-expanded:text-fg",
        destructive: "bg-critical-solid text-on-critical hover:bg-critical",
        link: "h-auto px-0 text-fg underline decoration-line-strong underline-offset-4 hover:decoration-fg",
      },
      size: {
        default: "h-(--vs-control) px-3.5 text-body",
        sm: "h-(--vs-control-sm) px-3 text-small",
        xs: "h-7 gap-1.5 rounded-sm px-2 text-caption [&_svg:not([class*='size-'])]:size-3.5",
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
