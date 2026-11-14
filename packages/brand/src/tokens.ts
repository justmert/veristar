// the veristar brand, as data. this file is the single source of every
// colour, type step, radius, shadow, motion value and layout size the
// console, the website and the umami theme use. `pnpm --filter
// @veristar/brand generate` writes the css from it; `check` fails when a
// committed css file no longer matches.
//
// two layers: `palette` holds raw values and is never referenced by a
// component. `themes` maps every semantic role to a palette value. a
// component only ever names a role (bg-raised, text-fg-2, stroke-series-1),
// so a new theme is one more entry in `themes` and nothing else changes.
//
// the brand ships dark only. to add a light theme: add `light` to `themes`
// with the same role keys (the type below makes a missing role an error),
// and run generate. it emits a [data-theme="light"] block; the app then sets
// that attribute on <html>.

export const palette = {
  neutral: {
    1000: "#080807",
    950: "#121211",
    900: "#151514",
    850: "#1c1c1a",
    800: "#22221f",
    780: "#232320",
    750: "#252522",
    700: "#2a2a27",
    650: "#2e2d29",
    600: "#3d3c37",
    500: "#6e6c65",
    400: "#918f86",
    300: "#a6a49c",
    200: "#d9d8d2",
    100: "#eeede8",
  },
  orange: { 400: "#ff9b6b", 500: "#ff7a3d", 950: "#1a0a02" },
  green: { 500: "#3fb27f", 800: "#2b4a3b", 950: "#0e2a1c" },
  amber: { 500: "#e0a54a", 800: "#4a3a1f", 950: "#2a2112" },
  red: { 400: "#f97066", 500: "#e5484d", 800: "#5a2a26", 900: "#2a1816", 950: "#1a0505" },
  // the data palette, validated with the dataviz validator on the raised
  // surface: worst adjacent cvd delta e 8.4, normal vision 19.7, every hue
  // at least 3:1 on the card
  blue: { 500: "#3987e5" },
  magenta: { 500: "#d55181" },
  violet: { 500: "#9085e9" },
  aqua: { 500: "#199e70" },
  ochre: { 500: "#c98500" },
} as const;

const p = palette;

// every role a theme must define. the comment says what the role is for, so
// a light theme maps meaning, not shades.
export type Roles = {
  // surfaces, from deepest to highest
  sunken: string; // inputs, code, wells inside a card
  ground: string; // the page
  raised: string; // cards, tables, the sidebar's selected project
  overlay: string; // menus, popovers, tooltips, dialogs
  hover: string; // a row or item under the pointer
  selected: string; // the current item, a pressed toggle
  muted: string; // skeletons, the track of a meter
  scrim: string; // behind a dialog
  // lines
  line: string; // every border by default
  "line-strong": string; // control borders, the chart baseline
  "line-subtle": string; // table row rules, chart grid
  // text
  fg: string;
  "fg-2": string;
  "fg-3": string;
  "fg-disabled": string;
  "on-accent": string; // text on the accent fill
  "on-critical": string; // text on the destructive fill
  // the one primary action; never a status. `accent` is the fill under
  // `on-accent` text; `accent-text` is the accent drawn on a dark surface
  // (links, markers, the focus ring, the mark's star), which needs a lighter
  // step than a fill does
  accent: string;
  "accent-hover": string;
  "accent-pressed": string;
  "accent-text": string;
  "accent-soft": string; // the focus halo
  // status, always shown with an icon and a word
  success: string;
  "success-soft": string;
  "success-line": string;
  warning: string;
  "warning-soft": string;
  "warning-line": string;
  critical: string;
  "critical-soft": string;
  "critical-line": string;
  "critical-solid": string; // the fill of a destructive button
  info: string;
  // data
  "series-1": string;
  "series-2": string;
  "series-3": string;
  "series-4": string;
  "series-5": string;
  "series-prev": string; // the previous period, dashed
  "series-focus": string; // the halo of a focused mark
  grid: string;
  axis: string;
};

export const themes = {
  dark: {
    sunken: p.neutral[950],
    ground: p.neutral[900],
    raised: p.neutral[850],
    overlay: p.neutral[800],
    hover: p.neutral[750],
    selected: p.neutral[700],
    muted: p.neutral[780],
    scrim: "rgb(8 8 7 / 0.6)",
    line: p.neutral[650],
    "line-strong": p.neutral[600],
    "line-subtle": p.neutral[750],
    fg: p.neutral[100],
    "fg-2": p.neutral[300],
    "fg-3": p.neutral[400],
    "fg-disabled": p.neutral[500],
    "on-accent": p.orange[950],
    "on-critical": p.red[950],
    accent: p.orange[500],
    "accent-hover": p.orange[400],
    "accent-pressed": p.orange[400],
    "accent-text": p.orange[500],
    "accent-soft": "rgb(255 122 61 / 0.25)",
    success: p.green[500],
    "success-soft": p.green[950],
    "success-line": p.green[800],
    warning: p.amber[500],
    "warning-soft": p.amber[950],
    "warning-line": p.amber[800],
    critical: p.red[400],
    "critical-soft": p.red[900],
    "critical-line": p.red[800],
    "critical-solid": p.red[500],
    info: p.neutral[300],
    "series-1": p.blue[500],
    "series-2": p.magenta[500],
    "series-3": p.violet[500],
    "series-4": p.aqua[500],
    "series-5": p.ochre[500],
    "series-prev": p.neutral[500],
    "series-focus": "rgb(57 135 229 / 0.25)",
    grid: p.neutral[750],
    axis: p.neutral[600],
  },
} as const satisfies Record<string, Roles>;

export type ThemeName = keyof typeof themes;
export const defaultTheme: ThemeName = "dark";

// css color-scheme of each theme, so native controls and scroll bars match
export const colorScheme: Record<ThemeName, "dark" | "light"> = { dark: "dark" };

export const font = {
  sans: '"Geist Variable", ui-sans-serif, system-ui, sans-serif',
  mono: '"Geist Mono Variable", ui-monospace, "SF Mono", monospace',
} as const;

// the type scale: size / line height, weight, tracking. numbers are set in
// mono at small, metric-sm and metric only.
export const type = {
  axis: { size: "11px", line: "14px", weight: 400, tracking: "0" },
  caption: { size: "12px", line: "16px", weight: 500, tracking: "0.01em" },
  small: { size: "13px", line: "20px", weight: 400, tracking: "0" },
  body: { size: "14px", line: "20px", weight: 400, tracking: "0" },
  title: { size: "16px", line: "24px", weight: 600, tracking: "0" },
  section: { size: "20px", line: "28px", weight: 600, tracking: "-0.01em" },
  page: { size: "28px", line: "36px", weight: 600, tracking: "-0.025em" },
  metric: { size: "28px", line: "36px", weight: 500, tracking: "-0.02em" },
  h1: { size: "32px", line: "40px", weight: 600, tracking: "-0.03em" },
  display: { size: "48px", line: "52px", weight: 600, tracking: "-0.035em" },
} as const;

export const radius = {
  bar: "2px", // the data end of a bar
  sm: "4px", // chips, segmented items, kbd
  md: "6px", // buttons, inputs, nav items
  lg: "10px", // cards
  xl: "12px", // panels, dialogs
  "2xl": "16px", // the website's hero frame
  full: "9999px",
} as const;

export const shadow = {
  overlay: "0 8px 24px rgb(0 0 0 / 0.32)",
  dialog: "0 16px 48px rgb(0 0 0 / 0.5)",
} as const;

// motion. every motion carries a meaning (system v2):
//   press    a click registered
//   reveal   a menu, tooltip or popover opens from its trigger
//   change   a value moved, a dialog opens
//   arrival  something new arrived (first event, new alert)
//   highlight the fading mark left by an arrival
//   live     the breathing dot of anything listening
// reduced motion keeps only opacity changes (see motion.ts).
export const motion = {
  duration: {
    press: 120,
    reveal: 180,
    change: 240,
    arrival: 240,
    page: 200,
    highlight: 1200,
    live: 2000,
    spin: 800,
  },
  ease: {
    standard: [0.2, 0, 0, 1],
    exit: [0.4, 0, 1, 1],
  },
  // the 4 px a revealed or arriving element travels
  distance: 4,
} as const;

export const layout = {
  sidebar: "248px",
  content: "1440px", // widest content column of the console, centred in the space beside the sidebar
  prose: "720px", // terms, docs-like reading width
  dialog: "552px", // 520 px panel plus the 16 px gutter on each side
  "dialog-sm": "472px",
  control: "36px",
  "control-sm": "32px",
  "control-touch": "44px",
  row: "36px", // compact table rows
  "row-comfortable": "48px",
  header: "56px",
} as const;
