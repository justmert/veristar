import { cn } from "@veristar/brand/cn";
import { transition } from "@veristar/brand/motion";
import { LayoutGroup, motion } from "motion/react";
import { useId } from "react";
import { Link, useLocation } from "react-router";

// the second level of a place as tabs of links; the marker slides between
// them. aria-current marks the page, as for any navigation.
export function NavTabs({
  label,
  items,
  className,
}: {
  label: string;
  items: { to: string; label: string; badge?: React.ReactNode }[];
  className?: string;
}) {
  const { pathname } = useLocation();
  const group = useId();
  // the longest matching prefix wins, so /api/traffic is not also /api
  const current = items
    .filter((i) => pathname === i.to || pathname.startsWith(`${i.to}/`))
    .sort((a, b) => b.to.length - a.to.length)[0]?.to;
  return (
    <LayoutGroup id={group}>
      <nav
        aria-label={label}
        className={cn("-mx-1 overflow-x-auto border-b border-line", className)}
      >
        <ul className="flex gap-1 px-1">
          {items.map((i) => {
            const on = i.to === current;
            return (
              <li key={i.to}>
                <Link
                  to={i.to}
                  aria-current={on ? "page" : undefined}
                  className={cn(
                    "relative inline-flex h-10 items-center gap-1.5 px-3 text-body font-medium whitespace-nowrap transition-colors duration-(--vs-duration-reveal) ease-standard focus-ring",
                    on ? "text-fg" : "text-fg-2 hover:text-fg",
                  )}
                >
                  {i.label}
                  {i.badge}
                  {on ? (
                    <motion.span
                      layoutId="navtab-marker"
                      transition={transition.layout}
                      aria-hidden
                      className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-accent-text"
                    />
                  ) : null}
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>
    </LayoutGroup>
  );
}
