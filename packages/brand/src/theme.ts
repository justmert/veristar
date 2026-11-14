// the person's theme choice: system, light or dark. "system" follows the
// operating system live. the choice is kept in this browser only, and the
// page still works when storage is refused. theme-boot.js applies the same
// rule before the first paint.
import { useCallback, useEffect, useState } from "react";

export type ThemeChoice = "system" | "light" | "dark";
const KEY = "veristar-theme";
const query = "(prefers-color-scheme: light)";

function read(): ThemeChoice {
  try {
    const v = localStorage.getItem(KEY);
    return v === "light" || v === "dark" ? v : "system";
  } catch {
    return "system";
  }
}

function apply(choice: ThemeChoice) {
  const resolved =
    choice === "system" ? (window.matchMedia(query).matches ? "light" : "dark") : choice;
  document.documentElement.dataset.theme = resolved;
}

export function useTheme() {
  const [choice, setChoice] = useState<ThemeChoice>(read);
  useEffect(() => {
    apply(choice);
    if (choice !== "system") return;
    const m = window.matchMedia(query);
    const on = () => apply("system");
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, [choice]);
  const set = useCallback((next: ThemeChoice) => {
    try {
      if (next === "system") localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, next);
    } catch {
      // not stored; the choice lasts until the page is reloaded
    }
    setChoice(next);
  }, []);
  return { choice, setChoice: set };
}
