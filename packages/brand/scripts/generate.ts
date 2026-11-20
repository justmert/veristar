// writes every css file derived from src/tokens.ts, or with --check verifies
// that the committed copies still match (run by `pnpm check`):
//
//   src/tokens.css            the --vs-* custom properties, one block per theme
//   src/theme.generated.css   the tailwind @theme that maps utilities to them
//   src/spacing.generated.css tailwind's spacing step from the spacing scale
//                             (imported by the console only, see below)
//   docs/docs.json            mintlify's accent and grounds
//   the mark as svg           favicons, the status page mark, the docs logos
//   public/theme.js           the theme boot script, copied into each app
//
// so a brand change is an edit of tokens.ts and one run of this script.

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  colorScheme,
  defaultTheme,
  font,
  layer,
  layout,
  motion,
  type Roles,
  radius,
  shadow,
  space,
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
  // a dot is not valid in a custom property name: escaped, the half steps
  // stay one declaration each instead of dropping the whole :root block in
  // the production css minifier
  for (const [k, v] of Object.entries(space))
    out.push(`  --vs-space-${k.replace(".", "\\.")}: ${v};`);
  for (const [k, v] of Object.entries(layer)) out.push(`  --vs-layer-${k}: ${v};`);
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

// spacing.generated.css: tailwind's spacing step becomes the scale's 4 px
// base, so p-4 is 16 px and every padding, margin and gap lands on the
// scale (check-usage refuses values outside it). the console imports it;
// the website keeps tailwind's own rem step under its 14 px root until it
// moves onto the scale on purpose, so it renders exactly as before.
{
  // the multiplier only holds when every step is its key times the base
  const base = Number.parseFloat(space[1]);
  for (const [k, v] of Object.entries(space)) {
    if (`${Number(k) * base}px` !== v) problems.push(`space ${k} is ${v}, not ${k} x ${base}px`);
  }
  const out = [HEAD, "@theme inline {", "  --spacing: var(--vs-space-1);", "}", ""];
  want(join(src, "spacing.generated.css"), out.join("\n"));
}

// the docs (mintlify) take the accent and both grounds; edited in place so
// the rest of docs.json keeps its formatting. mintlify's "primary" is the
// emphasis on its light theme, "light" the emphasis on its dark theme and
// "dark" the fill of its buttons in both.
{
  const d: Roles = themes.dark;
  const l: Roles = themes.light;
  const path = join(repo, "docs/docs.json");
  let text = readFileSync(path, "utf8");
  const up = (v: string) => v.toUpperCase();
  text = text.replace(
    /("colors":\s*\{\s*"primary":\s*")[^"]*(",\s*"light":\s*")[^"]*(",\s*"dark":\s*")[^"]*(")/,
    `$1${up(l["accent-text"])}$2${up(d["accent-text"])}$3${up(l.accent)}$4`,
  );
  text = text.replace(
    /("background":\s*\{\s*"color":\s*\{)[^}]*(\})/,
    `$1\n      "light": "${up(l.ground)}",\n      "dark": "${up(d.ground)}"\n    $2`,
  );
  want(path, text);
}

// the mark as svg files: the favicons (one dark tile everywhere), the status
// page's mark and the docs logo for each theme. the geometry is the one in
// src/mark.tsx: a soft star turned over, a round cut between its upper arms
// and a point held in the cut
{
  const d: Roles = themes.dark;
  const l: Roles = themes.light;
  const points =
    "24.00,40.00 20.47,33.35 13.06,32.05 18.29,26.65 9.01,7.87 24.00,22.50 38.99,7.87 29.71,26.65 34.94,32.05 27.53,33.35";
  const up = (v: string) => v.toUpperCase();
  const mark = (ink: string, id: string) =>
    `  <defs><mask id="${id}" maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">` +
    `<rect width="48" height="48" fill="#FFFFFF"/><circle cx="24" cy="16.5" r="7.5" fill="#000000"/></mask></defs>\n` +
    `  <polygon points="${points}" mask="url(#${id})" fill="${up(ink)}" stroke="${up(ink)}" stroke-width="4" stroke-linejoin="round"/>\n` +
    `  <circle cx="24" cy="16.5" r="3.4" fill="${up(ink)}"/>\n`;
  const head =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="32" height="32">\n';
  // the tile keeps the mark a little inside its edge
  const favicon =
    `${head}  <title>Veristar</title>\n` +
    `  <rect width="48" height="48" rx="12" fill="${up(d.ground)}"/>\n` +
    `  <g transform="translate(6 6) scale(0.75)">\n${mark(d.fg, "cut")}  </g>\n</svg>\n`;
  for (const f of [
    "apps/console/public/favicon.svg",
    "apps/site/public/favicon.svg",
    "docs/favicon.svg",
    "packages/brand/assets/favicon.svg",
  ]) {
    want(join(repo, f), favicon);
  }
  // the public status page shows the mark on its dark header
  want(
    join(repo, "packages/brand/assets/mark.svg"),
    `${head}  <title>Veristar</title>\n${mark(d.fg, "cut")}</svg>\n`,
  );
  const logo = (t: Roles) =>
    '<svg xmlns="http://www.w3.org/2000/svg" width="152" height="32" viewBox="0 0 152 32" role="img" aria-label="Veristar">\n' +
    "  <title>Veristar</title>\n" +
    '  <g transform="scale(0.6667)">\n' +
    mark(t.fg, "cut") +
    "  </g>\n" +
    `  <text x="38" y="23.5" font-family="Inter, 'Helvetica Neue', Arial, sans-serif" font-size="21" font-weight="600" letter-spacing="-0.6" fill="${up(t.fg)}">Veristar</text>\n` +
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
