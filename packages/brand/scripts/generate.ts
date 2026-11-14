// writes every css file derived from src/tokens.ts, or with --check verifies
// that the committed copies still match (run by `pnpm check`):
//
//   src/tokens.css           the --vs-* custom properties, one block per theme
//   src/theme.generated.css  the tailwind @theme that maps utilities to them
//   the --vs-* block of deploy/caddy/umami-theme/site/_veristar/theme.css
//
// so a brand change is an edit of tokens.ts and one run of this script.

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  colorScheme,
  defaultTheme,
  font,
  layout,
  motion,
  type Roles,
  radius,
  shadow,
  type ThemeName,
  themes,
  type,
} from "../src/tokens.ts";

const here = dirname(fileURLToPath(import.meta.url));
const src = join(here, "..", "src");
const repo = join(here, "..", "..", "..");
const check = process.argv.includes("--check");
const problems: string[] = [];

function want(path: string, text: string) {
  if (check) {
    if (!existsSync(path)) problems.push(`${path} is missing; run the generate script`);
    else if (readFileSync(path, "utf8") !== text) problems.push(`${path} differs from tokens.ts`);
    return;
  }
  writeFileSync(path, text);
}

const HEAD =
  "/* generated from packages/brand/src/tokens.ts by scripts/generate.ts; do not edit */\n";
const bezier = (e: readonly number[]) => `cubic-bezier(${e.join(", ")})`;
const roleNames = Object.keys(themes[defaultTheme]) as (keyof Roles)[];

function roleBlock(name: ThemeName) {
  const t: Roles = themes[name];
  const lines = roleNames.map((r) => `  --vs-${r}: ${t[r]};`);
  return [`  color-scheme: ${colorScheme[name]};`, ...lines].join("\n");
}

// tokens.css: static scales on :root, then one block per theme. the default
// theme also owns :root, so a page without data-theme gets it.
{
  const out: string[] = [HEAD, ":root {"];
  out.push(`  --vs-font-sans: ${font.sans};`, `  --vs-font-mono: ${font.mono};`);
  for (const [k, v] of Object.entries(type)) {
    out.push(
      `  --vs-text-${k}: ${v.size};`,
      `  --vs-text-${k}-line: ${v.line};`,
      `  --vs-text-${k}-weight: ${v.weight};`,
      `  --vs-text-${k}-tracking: ${v.tracking};`,
    );
  }
  for (const [k, v] of Object.entries(radius)) out.push(`  --vs-radius-${k}: ${v};`);
  for (const [k, v] of Object.entries(shadow)) out.push(`  --vs-shadow-${k}: ${v};`);
  for (const [k, v] of Object.entries(motion.duration)) out.push(`  --vs-duration-${k}: ${v}ms;`);
  for (const [k, v] of Object.entries(motion.ease)) out.push(`  --vs-ease-${k}: ${bezier(v)};`);
  out.push(`  --vs-motion-distance: ${motion.distance}px;`);
  for (const [k, v] of Object.entries(layout)) out.push(`  --vs-${k}: ${v};`);
  out.push("}", "");
  for (const name of Object.keys(themes) as ThemeName[]) {
    const sel = name === defaultTheme ? `:root,\n[data-theme="${name}"]` : `[data-theme="${name}"]`;
    out.push(`${sel} {`, roleBlock(name), "}", "");
  }
  // without a data-theme attribute (before the console's theme script runs,
  // or on the website) a light operating system gets the light theme
  if ("light" in themes) {
    const inner = roleBlock("light" as ThemeName)
      .split("\n")
      .map((l) => `  ${l}`)
      .join("\n");
    out.push(
      "@media (prefers-color-scheme: light) {",
      "  :root:not([data-theme]) {",
      inner,
      "  }",
      "}",
      "",
    );
  }
  want(join(src, "tokens.css"), out.join("\n"));
}

// theme.generated.css: the tailwind namespaces are cleared first, so only a
// brand token can become a utility. bg-red-500 or text-lg simply does not
// exist; scripts/check-usage.ts also fails on arbitrary values.
{
  const out: string[] = [HEAD, "@theme inline {"];
  for (const ns of [
    "color",
    "font",
    "text",
    "radius",
    "shadow",
    "inset-shadow",
    "drop-shadow",
    "text-shadow",
    "ease",
    "animate",
    "container",
    "blur",
  ]) {
    out.push(`  --${ns}-*: initial;`);
  }
  for (const r of roleNames) out.push(`  --color-${r}: var(--vs-${r});`);
  out.push("  --font-sans: var(--vs-font-sans);", "  --font-mono: var(--vs-font-mono);");
  for (const k of Object.keys(type)) {
    out.push(
      `  --text-${k}: var(--vs-text-${k});`,
      `  --text-${k}--line-height: var(--vs-text-${k}-line);`,
      `  --text-${k}--font-weight: var(--vs-text-${k}-weight);`,
      `  --text-${k}--letter-spacing: var(--vs-text-${k}-tracking);`,
    );
  }
  for (const k of Object.keys(radius)) out.push(`  --radius-${k}: var(--vs-radius-${k});`);
  for (const k of Object.keys(shadow)) out.push(`  --shadow-${k}: var(--vs-shadow-${k});`);
  for (const k of Object.keys(motion.ease)) out.push(`  --ease-${k}: var(--vs-ease-${k});`);
  for (const k of ["content", "prose", "dialog", "dialog-sm", "sidebar"] as const) {
    out.push(`  --container-${k}: var(--vs-${k});`);
  }
  out.push("  --blur-scrim: 2px;", "}", "");
  want(join(src, "theme.generated.css"), out.join("\n"));
}

// the umami theme keeps its own names for the same values (docs/adr/0004)
{
  const t: Roles = themes.dark;
  const vs = {
    background: t.ground,
    card: t.raised,
    popover: t.overlay,
    muted: t.muted,
    accent: t.hover,
    selected: t.selected,
    border: t.line,
    input: t["line-strong"],
    foreground: t.fg,
    "muted-foreground": t["fg-2"],
    "disabled-foreground": t["fg-disabled"],
    primary: t.accent,
    "primary-hover": t["accent-hover"],
    "primary-foreground": t["on-accent"],
    "primary-text": t["accent-text"],
    ring: t["accent-text"],
    success: t.success,
    warning: t.warning,
    destructive: t.critical,
    "destructive-solid": t["critical-solid"],
    "destructive-foreground": t["on-critical"],
    "font-sans": font.sans,
    "font-mono": font.mono,
    "radius-control": "0.375rem",
    "radius-card": "0.625rem",
    "radius-overlay": "0.75rem",
    "shadow-floating": shadow.overlay.replace("var(--vs-shadow-soft)", t["shadow-soft"]),
  };
  if (radius.md !== "6px" || radius.lg !== "10px" || radius.xl !== "12px") {
    throw new Error("umami radius tokens are written in rem; update them with radius md, lg, xl");
  }
  const path = join(repo, "deploy/caddy/umami-theme/site/_veristar/theme.css");
  const cur = readFileSync(path, "utf8");
  const open = "/* brand tokens, the only values to edit when the brand changes */\nhtml:root {\n";
  const start = cur.indexOf(open);
  const end = cur.indexOf("\n}\n", start);
  if (start < 0 || end < 0) throw new Error(`${path} has no brand token block`);
  const block = Object.entries(vs)
    .map(([k, v]) => `  --vs-${k}: ${v};`)
    .join("\n");
  want(path, cur.slice(0, start + open.length) + block + cur.slice(end));
}

// the docs (mintlify) take the accent and both grounds; edited in place so
// the rest of docs.json keeps its formatting. mintlify's "light" colour is
// the one it draws on the dark theme and "dark" the one on the light theme.
{
  const d: Roles = themes.dark;
  const l: Roles = themes.light;
  const path = join(repo, "docs/docs.json");
  let text = readFileSync(path, "utf8");
  const up = (v: string) => v.toUpperCase();
  text = text.replace(
    /("colors":\s*\{\s*"primary":\s*")[^"]*(",\s*"light":\s*")[^"]*(",\s*"dark":\s*")[^"]*(")/,
    `$1${up(d.accent)}$2${up(d["accent-text"])}$3${up(l["accent-text"])}$4`,
  );
  text = text.replace(
    /("background":\s*\{\s*"color":\s*\{)[^}]*(\})/,
    `$1\n      "light": "${up(l.ground)}",\n      "dark": "${up(d.ground)}"\n    $2`,
  );
  want(path, text);
}

// the mark as svg files: the favicons (one dark tile everywhere), the umami
// sidebar mark and the docs logo for each theme
{
  const d: Roles = themes.dark;
  const l: Roles = themes.light;
  const star = "M16 1.5 L17.3 4.7 L20.5 6 L17.3 7.3 L16 10.5 L14.7 7.3 L11.5 6 L14.7 4.7 Z";
  const up = (v: string) => v.toUpperCase();
  const mark = (ink: string, accent: string) =>
    `  <circle cx="16" cy="17" r="11" fill="none" stroke="${up(ink)}" stroke-width="2.4"/>\n` +
    `  <path d="M16 6v22" fill="none" stroke="${up(ink)}" stroke-width="2.4"/>\n` +
    `  <path d="${star}" fill="${up(accent)}"/>\n`;
  const head =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32">\n';
  const favicon =
    `${head}  <title>Veristar</title>\n` +
    `  <rect width="32" height="32" rx="8" fill="${up(d.ground)}"/>\n` +
    `${mark(d.fg, d["accent-text"])}</svg>\n`;
  for (const f of [
    "apps/console/public/favicon.svg",
    "apps/site/public/favicon.svg",
    "docs/favicon.svg",
    "deploy/caddy/umami-theme/site/_veristar/brand/favicon.svg",
  ]) {
    want(join(repo, f), favicon);
  }
  want(
    join(repo, "deploy/caddy/umami-theme/site/_veristar/brand/mark.svg"),
    `${head}  <title>Veristar</title>\n${mark(d.fg, d["accent-text"])}</svg>\n`,
  );
  const logo = (t: Roles) =>
    '<svg xmlns="http://www.w3.org/2000/svg" width="152" height="32" viewBox="0 0 152 32" role="img" aria-label="Veristar">\n' +
    "  <title>Veristar</title>\n" +
    mark(t.fg, t["accent-text"]) +
    `  <text x="36" y="23.5" font-family="Inter, 'Helvetica Neue', Arial, sans-serif" font-size="21" font-weight="600" letter-spacing="-0.6" fill="${up(t.fg)}">Veristar</text>\n` +
    "</svg>\n";
  want(join(repo, "docs/logo/veristar.svg"), logo(d));
  want(join(repo, "docs/logo/veristar-light.svg"), logo(l));
}

// the theme boot script, copied into each app's public folder
{
  const boot = readFileSync(join(src, "theme-boot.js"), "utf8");
  for (const f of ["apps/console/public/theme.js", "apps/site/public/theme.js"]) {
    want(join(repo, f), boot);
  }
}

if (problems.length) {
  for (const p of problems) console.error(p);
  process.exit(1);
}
