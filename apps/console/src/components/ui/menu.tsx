import { cn } from "@veristar/brand/cn";
import { Check } from "lucide-react";
import { DropdownMenu as M } from "radix-ui";
import type * as React from "react";
import { createContext, useContext } from "react";

// a menu opened inside a native modal <dialog> (the phone drawer) must
// render inside that dialog: the dialog makes the rest of the page inert, so
// a menu portalled to <body> would show behind it and take no input. the
// dialog provides itself here as the portal's container
export const MenuContainer = createContext<HTMLElement | null>(null);

// a dropdown menu. modal={false} keeps radix from adding its scroll lock,
// which needs an inline <style> element the csp refuses.
function Menu({ children, ...props }: React.ComponentProps<typeof M.Root>) {
  return (
    <M.Root modal={false} {...props}>
      {children}
    </M.Root>
  );
}

const MenuTrigger = M.Trigger;

function MenuContent({
  className,
  align = "start",
  sideOffset = 6,
  ...props
}: React.ComponentProps<typeof M.Content>) {
  const container = useContext(MenuContainer);
  return (
    <M.Portal container={container ?? undefined}>
      <M.Content
        align={align}
        sideOffset={sideOffset}
        collisionPadding={8}
        className={cn(
          "z-(--vs-layer-overlay) min-w-52 overflow-hidden rounded-[10px] border border-line-2 bg-overlay p-1 text-fg shadow-overlay data-[state=closed]:animate-reveal-out data-[state=open]:animate-reveal-in",
          className,
        )}
        {...props}
      />
    </M.Portal>
  );
}

function MenuItem({
  className,
  tone,
  ...props
}: React.ComponentProps<typeof M.Item> & { tone?: "critical" }) {
  return (
    <M.Item
      className={cn(
        "flex h-8 cursor-pointer select-none items-center gap-2.5 rounded-[6px] px-2 text-body outline-none max-lg:h-(--vs-control-touch) transition-colors duration-(--vs-duration-press) data-[disabled]:pointer-events-none data-[highlighted]:bg-hover data-[disabled]:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-3",
        tone === "critical" && "text-critical [&_svg]:text-critical",
        className,
      )}
      {...props}
    />
  );
}

function MenuLabel({ className, ...props }: React.ComponentProps<typeof M.Label>) {
  return <M.Label className={cn("px-2 pt-1 pb-1 text-caption text-fg-3", className)} {...props} />;
}

function MenuSeparator({ className, ...props }: React.ComponentProps<typeof M.Separator>) {
  return <M.Separator className={cn("-mx-1 my-1 h-px bg-line", className)} {...props} />;
}

const MenuRadioGroup = M.RadioGroup;

// a choice among a few inside a menu; the check shows the current one
function MenuRadioItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof M.RadioItem>) {
  return (
    <M.RadioItem
      className={cn(
        "flex h-8 cursor-pointer select-none items-center gap-2.5 rounded-[6px] px-2 text-body outline-none max-lg:h-(--vs-control-touch) transition-colors duration-(--vs-duration-press) data-[highlighted]:bg-hover [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-3",
        className,
      )}
      {...props}
    >
      {children}
      <M.ItemIndicator className="ml-auto">
        <Check aria-hidden className="text-fg-2" />
      </M.ItemIndicator>
    </M.RadioItem>
  );
}

// an on or off choice inside a menu, such as a column of a table; the menu
// stays open so several can be changed in a row
function MenuCheckboxItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof M.CheckboxItem>) {
  return (
    <M.CheckboxItem
      onSelect={(e) => e.preventDefault()}
      className={cn(
        "flex h-8 cursor-pointer select-none items-center gap-2.5 rounded-[6px] px-2 text-body outline-none max-lg:h-(--vs-control-touch) transition-colors duration-(--vs-duration-press) data-[disabled]:pointer-events-none data-[highlighted]:bg-hover data-[disabled]:text-disabled-fg [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-2",
        className,
      )}
      {...props}
    >
      <span className="flex size-4 items-center justify-center">
        <M.ItemIndicator>
          <Check aria-hidden />
        </M.ItemIndicator>
      </span>
      {children}
    </M.CheckboxItem>
  );
}

export {
  Menu,
  MenuCheckboxItem,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuRadioGroup,
  MenuRadioItem,
  MenuSeparator,
  MenuTrigger,
};
