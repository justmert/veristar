import { Mark } from "@veristar/brand/mark";
import { reveal } from "@veristar/brand/motion";
import { Menu, X } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useId, useState } from "react";
import { site } from "../links";
import { action } from "./ui";

type NavLink = { label: string; href: string };

// on the 404 page the section anchors live on the home page
function navLinks(onHome: boolean): NavLink[] {
  const at = (hash: string) => (onHome ? hash : `/${hash}`);
  const out: NavLink[] = [
    { label: "How it works", href: at("#how") },
    { label: "Privacy", href: at("#privacy") },
    { label: "Handover", href: at("#handover") },
  ];
  const docs = site.docs();
  if (docs) out.push({ label: "Docs", href: docs });
  if (site.status) out.push({ label: "Status", href: site.status });
  return out;
}

const navItem =
  "rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring";

export function Header({ onHome = true }: { onHome?: boolean }) {
  const [open, setOpen] = useState(false);
  const menuId = useId();
  const items = navLinks(onHome);

  // escape closes the phone menu, as every disclosure in the console does
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <header className="sticky top-0 z-10 border-b border-line bg-ground">
      <div className="mx-auto flex min-h-14 max-w-content items-center gap-3 pr-2 pl-4 sm:min-h-16 sm:gap-7 sm:px-6">
        <a
          href={onHome ? "#top" : "/"}
          aria-label="Veristar home"
          className="flex items-center gap-2 rounded-sm focus-ring"
        >
          <Mark className="size-6" />
          <span className="text-title tracking-tight">Veristar</span>
        </a>
        <nav aria-label="Site" className="hidden items-center gap-6 text-body md:flex">
          {items.map((l) => (
            <a key={l.label} href={l.href} className={navItem}>
              {l.label}
            </a>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-1">
          {site.signIn && (
            <a
              href={site.signIn}
              className={`${action.outline} max-md:border-transparent max-md:px-3 md:h-(--vs-control) md:px-3.5`}
            >
              Sign in
            </a>
          )}
          <button
            type="button"
            className="inline-flex size-(--vs-control-touch) items-center justify-center rounded-md text-fg transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover focus-ring md:hidden"
            aria-expanded={open}
            aria-controls={menuId}
            aria-label={open ? "Close menu" : "Open menu"}
            onClick={() => setOpen((v) => !v)}
          >
            {open ? <X className="size-5" aria-hidden /> : <Menu className="size-5" aria-hidden />}
          </button>
        </div>
      </div>
      <AnimatePresence>
        {open && (
          <motion.nav
            id={menuId}
            aria-label="Site"
            initial={reveal.initial}
            animate={reveal.animate}
            exit={reveal.exit}
            className="border-t border-line px-4 pb-3 md:hidden"
          >
            <ul className="flex flex-col">
              {items.map((l) => (
                <li key={l.label} className="border-b border-line-subtle last:border-b-0">
                  <a
                    href={l.href}
                    onClick={() => setOpen(false)}
                    className="flex h-(--vs-control-touch) items-center rounded-sm text-body text-fg focus-ring"
                  >
                    {l.label}
                  </a>
                </li>
              ))}
            </ul>
          </motion.nav>
        )}
      </AnimatePresence>
    </header>
  );
}
