import { cn } from "@veristar/brand/cn";
import { transition } from "@veristar/brand/motion";
import { cva } from "class-variance-authority";
import { LayoutGroup, motion } from "motion/react";
import { Tabs as TabsPrimitive } from "radix-ui";
import type * as React from "react";
import { createContext, useContext, useId, useState } from "react";

// the active tab's marker slides between triggers (a shared layout id), so a
// switch reads as one control moving, not two repaints
const Ctx = createContext<{
  value: string | undefined;
  variant: "default" | "line";
  group: string;
}>({
  value: undefined,
  variant: "default",
  group: "",
});

function Tabs({
  className,
  value,
  defaultValue,
  onValueChange,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  const [inner, setInner] = useState(defaultValue);
  const current = value ?? inner;
  const group = useId();
  return (
    <Ctx.Provider value={{ value: current, variant: "default", group }}>
      <TabsPrimitive.Root
        data-slot="tabs"
        value={current}
        onValueChange={(v) => {
          setInner(v);
          onValueChange?.(v);
        }}
        className={cn("flex flex-col gap-4", className)}
        {...props}
      />
    </Ctx.Provider>
  );
}

const tabsListVariants = cva("relative inline-flex w-fit items-center text-fg-2", {
  variants: {
    variant: {
      default: "gap-1 rounded-md border border-line bg-sunken p-1",
      line: "w-full gap-1 border-b border-line",
    },
  },
  defaultVariants: { variant: "default" },
});

function TabsList({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List> & { variant?: "default" | "line" }) {
  const ctx = useContext(Ctx);
  return (
    <Ctx.Provider value={{ ...ctx, variant: variant ?? "default" }}>
      <LayoutGroup id={ctx.group}>
        <TabsPrimitive.List
          data-slot="tabs-list"
          data-variant={variant}
          className={cn(tabsListVariants({ variant }), className)}
          {...props}
        />
      </LayoutGroup>
    </Ctx.Provider>
  );
}

function TabsTrigger({
  className,
  children,
  value,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  const ctx = useContext(Ctx);
  const active = ctx.value === value;
  const line = ctx.variant === "line";
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      value={value}
      className={cn(
        "relative inline-flex items-center justify-center gap-1 whitespace-nowrap font-medium transition-colors duration-(--vs-duration-reveal) ease-standard hover:text-fg focus-ring disabled:pointer-events-none disabled:opacity-50 data-[state=active]:text-fg [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        line ? "h-10 px-3 text-body" : "h-7 rounded-sm px-2 text-small",
        className,
      )}
      {...props}
    >
      {active ? (
        <motion.span
          layoutId="tab-marker"
          transition={transition.layout}
          aria-hidden
          className={
            line
              ? "absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-text"
              : "absolute inset-0 rounded-sm bg-selected"
          }
        />
      ) : null}
      <span className="relative inline-flex items-center gap-1">{children}</span>
    </TabsPrimitive.Trigger>
  );
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("flex-1 animate-rise-in outline-none", className)}
      {...props}
    />
  );
}

export { Tabs, TabsContent, TabsList, TabsTrigger, tabsListVariants };
