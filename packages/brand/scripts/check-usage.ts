// fails when ui code uses a value that is not a brand token: a literal
// colour, an arbitrary tailwind value, a colour utility that names no role,
// a font size outside the type scale, a literal duration or motion value,
// or (in the console) a padding, margin or gap outside the spacing scale.
// tokens.ts is the only place such values may appear, so a theme change
// cannot miss one.

import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { motion, radius, shadow, space, themes, type } from "../src/tokens.ts";

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, "..", "..", "..");
const roots = ["apps/console/src", "apps/site/src", "packages/brand/src"];
const skip = new Set([
  "packages/brand/src/tokens.ts",
  "packages/brand/src/tokens.css",
  "packages/brand/src/theme.generated.css",
]);

// the console takes its spacing from the scale (spacing.generated.css). the
// website keeps tailwind's own step, so the spacing rules apply to the
// console only.
const spacingRoots = ["apps/console/src"];
const steps4 = new Set(Object.keys(space));
const spacingUtility =
  /(?<![\w-])(?:[a-z-]+:)*!?-?((?:p|px|py|pt|pr|pb|pl|ps|pe|m|mx|my|mt|mr|mb|ml|ms|me|gap|gap-x|gap-y|space-x|space-y|scroll-m[xytrblse]?|scroll-p[xytrblse]?))-(\[[^\]\s]+\]|\([^)\s]+\)|[0-9.]+)(?![\w.-])/g;
// a motion value written as a number in code: the presets in motion.ts
// build every one of them from tokens.ts
const motionLiteral =
  /\b(staggerChildren|delayChildren|stiffness|damping|mass|duration|delay|bounce)\s*:\s*-?\d/;

const roles = new Set(Object.keys(themes.dark));
const steps = new Set(Object.keys(type));
const radii = new Set(Object.keys(radius));
const shadows = new Set(Object.keys(shadow));

// suffixes of colour-looking prefixes that are not colours
const NOT_COLOUR: Record<string, RegExp> = {
  // the direction of a gradient; its stops (from-*, to-*) name the roles
  bg: /^linear-to-(t|tr|r|br|b|bl|l|tl)$/,
  text: /^(left|center|right|start|end|justify|wrap|nowrap|balance|pretty|ellipsis|clip)$/,
  border:
    /^(\d+|x|y|t|b|l|r|s|e|x-\d+|y-\d+|t-\d+|b-\d+|l-\d+|r-\d+|s-\d+|e-\d+|solid|dashed|dotted|double|hidden|none|collapse|separate|spacing.*)$/,
  ring: /^(\d+|inset|offset-\d+)$/,
  outline: /^(\d+|none|hidden|dashed|dotted|double|solid|offset-\d+)$/,
  stroke: /^(\d+|none)$/,
  fill: /^(none)$/,
  divide: /^(x|y|x-\d+|y-\d+|solid|dashed|dotted|double|none|x-reverse|y-reverse)$/,
  decoration: /^(\d+|solid|double|dotted|dashed|wavy|from-font|clone|slice|auto)$/,
  shadow: /^(none)$/,
  from: /^\d+%$/,
  via: /^\d+%$/,
  to: /^\d+%$/,
  caret: /^$/,
  placeholder: /^$/,
};
const KEYWORDS = /^(transparent|current|inherit|none)$/;

type Problem = { file: string; line: number; msg: string };
const problems: Problem[] = [];

function walk(dir: string): string[] {
  const out: string[] = [];
  let names: string[];
  try {
    names = readdirSync(dir);
  } catch {
    return out; // a root that does not exist yet (apps/site before it is built)
  }
  for (const n of names) {
    const p = join(dir, n);
    if (statSync(p).isDirectory()) out.push(...walk(p));
    else if (/\.(tsx?|css)$/.test(n)) out.push(p);
  }
  return out;
}

const literalColour =
  /(#[0-9a-fA-F]{3,8}\b(?![-\w])|\b(rgba?|hsla?|oklch|oklab|lab|lch|color-mix)\()/;
// layout may use arbitrary values (a grid template, a width); anything the
// brand defines may not
const arbitrary =
  /(?<![\w-])(?:[a-z-]+:)*!?(?:bg|text|border(?:-[xytblrse])?|ring|outline|fill|stroke|shadow|rounded(?:-[a-z]{1,2})?|font|tracking|leading|duration|delay|ease|animate|from|via|to|decoration|divide|caret|accent|placeholder)-\[[^\]\s]+\]/;
const utility =
  /(?<![\w-])(?:[a-z-]+:)*!?(bg|text|border(?:-[xytblrse])?|ring|outline|fill|stroke|divide|decoration|shadow|from|via|to|caret|placeholder)-([a-z0-9-]+%?)(?:\/\d+)?(?![\w-])/g;

for (const root of roots) {
  for (const file of walk(join(repo, root))) {
    const rel = relative(repo, file);
    if (skip.has(rel)) continue;
    const isCss = rel.endsWith(".css");
    const lines = readFileSync(file, "utf8").split("\n");
    lines.forEach((text, i) => {
      const at = (msg: string) => problems.push({ file: rel, line: i + 1, msg });
      const code = text.replace(/\/\/.*$/, "").replace(/\/\*.*?\*\//g, "");
      if (literalColour.test(code)) at("literal colour; use a role from tokens.ts");
      if (
        /\b(?<![\d.])(?!0\.01ms)\d+(\.\d+)?m?s\b/.test(code) &&
        /(duration|delay|transition|animation)/i.test(code)
      )
        if (!rel.endsWith("motion.ts")) at("literal duration; use a motion token");
      if (isCss) return;
      if (motionLiteral.test(code)) at("literal motion value; use a preset from motion.ts");
      if (spacingRoots.some((r) => rel.startsWith(r))) {
        for (const m of code.matchAll(spacingUtility)) {
          const value = m[2] ?? "";
          if (value.startsWith("[") || value.startsWith("("))
            at(`${m[0].trim()} is an arbitrary spacing value; use a step of the spacing scale`);
          else if (!steps4.has(value))
            at(`${m[0].trim()} is not a step of the spacing scale (${[...steps4].join(", ")})`);
        }
      }
      if (arbitrary.test(code)) at("arbitrary value for a brand property; use a token");
      if (/\b(duration|delay)-\d/.test(code))
        at("literal duration utility; use duration-(--vs-duration-*)");
      if (/\bease-(linear|in|out|in-out)\b/.test(code))
        at("tailwind easing; use ease-standard or ease-exit");
      for (const m of code.matchAll(utility)) {
        const [, prefix = "", value = ""] = m;
        const base = prefix.startsWith("border") ? "border" : prefix;
        if (KEYWORDS.test(value)) continue;
        if (base === "text" && steps.has(value)) continue;
        if (base === "shadow" && shadows.has(value)) continue;
        if (NOT_COLOUR[base]?.test(value)) continue;
        if (roles.has(value)) continue;
        // `rounded-*` is not matched above; text-* and friends that name
        // neither a role nor a step are a value outside the brand
        at(`${m[0].trim()} names no brand token`);
      }
      for (const m of code.matchAll(/(?<![\w-])rounded(?:-[trblse]{1,2})?-([a-z0-9]+)(?![\w-])/g)) {
        if (!radii.has(m[1] ?? "") && m[1] !== "none") at(`rounded-${m[1]} is not a radius token`);
      }
    });
  }
}

void motion;
if (problems.length) {
  for (const p of problems) console.error(`${p.file}:${p.line}: ${p.msg}`);
  console.error(`\n${problems.length} value(s) outside the brand tokens`);
  process.exit(1);
}
