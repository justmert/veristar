// sets data-theme on <html> before the first paint, from the person's saved
// choice or the operating system, so a page never flashes the wrong theme.
// loaded as a same-origin file (the console's csp allows no inline script).
// generated copies: apps/console/public/theme.js, apps/site/public/theme.js.
(() => {
  let choice = null;
  try {
    choice = localStorage.getItem("veristar-theme");
  } catch {
    // storage refused (private mode, blocked site data): follow the system
  }
  const light = window.matchMedia("(prefers-color-scheme: light)").matches;
  const theme = choice === "light" || choice === "dark" ? choice : light ? "light" : "dark";
  document.documentElement.dataset.theme = theme;
})();
