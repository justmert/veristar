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
    ring: t.accent,
    success: t.success,
    warning: t.warning,
    destructive: t.critical,
    "destructive-solid": t["critical-solid"],
    "font-sans": font.sans,
    "font-mono": font.mono,
    "radius-control": "0.375rem",
    "radius-card": "0.625rem",
    "radius-overlay": "0.75rem",
    "shadow-floating": shadow.overlay,
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

if (problems.length) {
  for (const p of problems) console.error(p);
  process.exit(1);
}
