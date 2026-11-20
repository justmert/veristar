// the public demo: a tab that opened /demo reads the sample projects through
// /api/demo, with no session. the flag lives in sessionStorage, so it belongs
// to that one tab: other tabs keep the real session, and closing the tab
// ends the demo. nothing in the demo can change data, so every write control
// asks this module before it draws.

import { useSyncExternalStore } from "react";

const KEY = "sa-console.demo";
// enter and leave happen in this tab; the storage event only fires in others
const CHANGED = "sa-console.demo-changed";

export function isDemo(): boolean {
  try {
    return window.sessionStorage.getItem(KEY) === "1";
  } catch {
    // storage refused (blocked site data): no demo, the console stays signed-in only
    return false;
  }
}

function setDemo(on: boolean) {
  try {
    if (on) window.sessionStorage.setItem(KEY, "1");
    else window.sessionStorage.removeItem(KEY);
  } catch {
    // storage refused; isDemo answers false, which is the signed-in console
  }
  window.dispatchEvent(new Event(CHANGED));
}

export function enterDemo() {
  setDemo(true);
}

export function leaveDemo() {
  setDemo(false);
}

// for a link that leaves the demo with a full page load: the flag goes, but
// this page is not told, so it does not redraw as a signed-out console (and
// send the visitor to sign-in with a return address) before the browser
// follows the link
export function leaveDemoOnNavigation() {
  try {
    window.sessionStorage.removeItem(KEY);
  } catch {
    // storage refused; the next page reads no demo either
  }
}

function subscribe(onChange: () => void) {
  window.addEventListener(CHANGED, onChange);
  return () => window.removeEventListener(CHANGED, onChange);
}

export function useDemo(): boolean {
  return useSyncExternalStore(subscribe, isDemo, () => false);
}

// the routes that need no session answer the same in and out of the demo,
// and /api/demo does not serve them
const SHARED = [/^\/terms(?:[/?]|$)/, /^\/public\//];

// where the api client sends a path: the demo prefix in a demo tab, except
// for the shared public routes
export function apiBase(path: string, demo: boolean = isDemo()): string {
  if (!demo || SHARED.some((r) => r.test(path))) return "/api";
  return "/api/demo";
}
