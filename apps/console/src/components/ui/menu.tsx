import { cn } from "@veristar/brand/cn";
import { DropdownMenu as M } from "radix-ui";
import type * as React from "react";

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
  return (
    <M.Portal>
      <M.Content
        align={align}
        sideOffset={sideOffset}
        collisionPadding={8}
        className={cn(
          "z-50 min-w-52 overflow-hidden rounded-lg border border-line-strong bg-overlay p-1 text-fg shadow-overlay data-[state=closed]:animate-reveal-out data-[state=open]:animate-reveal-in",
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
        "flex h-8 cursor-pointer select-none items-center gap-2.5 rounded-sm px-2 text-body outline-none transition-colors duration-(--vs-duration-press) data-[disabled]:pointer-events-none data-[highlighted]:bg-hover data-[disabled]:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-fg-3",
        tone === "critical" && "text-critical [&_svg]:text-critical",
        className,
      )}
      {...props}
    />
  );
}

function MenuLabel({ className, ...props }: React.ComponentProps<typeof M.Label>) {
  return (
    <M.Label className={cn("px-2 pt-1.5 pb-1 text-caption text-fg-3", className)} {...props} />
  );
}

function MenuSeparator({ className, ...props }: React.ComponentProps<typeof M.Separator>) {
  return <M.Separator className={cn("-mx-1 my-1 h-px bg-line", className)} {...props} />;
}

export { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger };
