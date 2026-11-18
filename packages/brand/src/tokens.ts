// the veristar brand, as data. this file is the single source of every
// colour, type step, spacing step, radius, shadow, motion value, layer and
// layout size the console and the website use. `pnpm --filter
// @veristar/brand generate` writes the css from it; `check` fails when a
// committed css file no longer matches.
//
// two layers: `palette` holds raw values and is never referenced by a
// component. `themes` maps every semantic role to a palette value. a
// component only ever names a role (bg-raised, text-fg-2, stroke-series-1),
// so a new theme is one more entry in `themes` and nothing else changes.
//
// two themes ship, dark (the default) and light. generate emits one block
// per theme under [data-theme="<name>"], with dark also on :root and light
// also under prefers-color-scheme: light when no data-theme is set. a third
// theme is one more entry in `themes` with the same role keys (the type
// below makes a missing role an error).

export const palette = {
  neutral: {
    1000: "#080807",
    950: "#121211",
    900: "#151514",
    850: "#1c1c1a",
    800: "#22221f",
    750: "#252522",
    700: "#2a2a27",
    650: "#2e2d29",
    625: "#33322e",
    600: "#3d3c37",
    550: "#737169",
    500: "#6e6c65",
    450: "#97958c",
    400: "#918f86",
    300: "#a6a49c",
    200: "#d9d8d2",
    100: "#eeede8",
  },
  // terracotta, the accent: a muted earth red that carries white text
  terracotta: { 300: "#f29a80", 500: "#b5482e", 600: "#a13f28", 700: "#8a3622" },
  white: "#ffffff",
  green: { 500: "#3fb27f", 800: "#2b4a3b", 950: "#0e2a1c" },
  amber: { 500: "#e0a54a", 800: "#4a3a1f", 950: "#2a2112" },
  // crimson, the destructive and critical hue: cooler than the terracotta
  // accent, so a destructive action never reads as the primary one. oklab
  // distance to the accent (x100, normal / protan / deutan): solid 11.0 /
  // 12.3 / 9.1, light text to accent-text 10.8 / 12.2 / 8.8, dark text to
  // accent-text 11.8 / 12.0 / 8.3
  crimson: {
    300: "#f26a90",
    500: "#a81650",
    600: "#940543",
    700: "#5f2937",
    900: "#31151c",
    lightSoft: "#fee8ec",
    lightLine: "#f5c1cc",
  },
  // the light theme's neutrals: a warm paper ground under white cards
  paper: {
    0: "#ffffff",
    50: "#f6f5f2",
    100: "#efeee9",
    150: "#ebe9e4",
    200: "#e5e3dd",
    250: "#e3e1db",
    275: "#d9d6cf",
    300: "#c4c1b9",
    400: "#a3a099",
    500: "#89867d",
    525: "#84817a",
    600: "#6b6961",
    650: "#66645c",
    700: "#57554e",
    800: "#3b3a35",
    850: "#2a2925",
    900: "#1a1917",
  },
  // status and data steps for a light surface, each at least 4.5:1 (text)
  // or 3:1 (marks) on white
  light: {
    success: "#1a6a42",
    successSoft: "#e4f2ea",
    successLine: "#b5dbc5",
    // an amber that stays apart from critical (4xx next to 5xx): 5.22 on
    // white, 4.79 on the ground
    warning: "#a35a00",
    warningSoft: "#fbf1dd",
    warningLine: "#ebd09c",
  },
  // the data palette, validated with the dataviz validator on the raised
  // surface: worst adjacent cvd delta e 8.4, normal vision 19.7, every hue
  // at least 3:1 on the card
  blue: { 500: "#3987e5" },
  magenta: { 500: "#d55181" },
  violet: { 500: "#9085e9" },
  aqua: { 500: "#199e70" },
  ochre: { 500: "#c98500" },
  // the same five hues stepped for a light surface (validator, light mode,
  // surface #fcfcfb: worst adjacent cvd delta e 9.5, normal vision 17.5, all
  // at least 3:1)
  seriesLight: ["#2a6fd6", "#c23f78", "#6e5ed6", "#14876a", "#a66b00"],
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
  // skeletons, the track of a meter: at least 1.3:1 on the card and the page
  muted: string;
  scrim: string; // behind a dialog
  // lines
  line: string; // every border by default
  "line-strong": string; // control borders, the chart baseline
  "line-subtle": string; // table row rules, chart grid
  // the edge of a form control (input, select, checkbox): at least 3:1
  // against the control's fill and every surface it sits on (wcag 1.4.11)
  "line-input": string;
  // text
  fg: string;
  "fg-2": string;
  // the quietest readable text: at least 4.5:1 on every surface up to
  // selected, so a caption keeps its contrast in a selected row
  "fg-3": string;
  "fg-disabled": string;
  // a control that cannot be used: a flat neutral fill, quiet text and a
  // quiet edge, so a disabled primary never reads as a dim accent and a
  // disabled outline never reads as enabled
  "disabled-fill": string;
  "disabled-fg": string;
  "disabled-line": string;
  // a toggle that is on (a chart option, a table view): the surface and its
  // edge
  pressed: string;
  "pressed-line": string;
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
  // the fill of the confirm button of a destructive dialog, and its hover;
  // a destructive action anywhere else is drawn in `critical` text with an
  // icon, never a fill
  "critical-solid": string;
  "critical-solid-hover": string;
  info: string;
  // data
  "series-1": string;
  "series-2": string;
  "series-3": string;
  "series-4": string;
  "series-5": string;
  "series-prev": string; // the previous period, dashed
  "series-focus": string; // the halo of a focused mark
  // the stripes cut into a bar of a period that has not ended (a partial
  // day or hour), so a half period never reads as a drop
  "series-partial": string;
  grid: string;
  axis: string;
  // elevation, the colour of an overlay's shadow
  "shadow-soft": string;
  "shadow-strong": string;
};

export const themes = {
  dark: {
    sunken: p.neutral[950],
    ground: p.neutral[900],
    raised: p.neutral[850],
    overlay: p.neutral[800],
    hover: p.neutral[750],
    selected: p.neutral[700],
    // 1.33 on raised, 1.42 on ground
    muted: p.neutral[625],
    scrim: "rgb(8 8 7 / 0.6)",
    line: p.neutral[650],
    "line-strong": p.neutral[600],
    "line-subtle": p.neutral[750],
    // 3.83 on sunken, 3.49 on raised, 3.26 on overlay
    "line-input": p.neutral[550],
    fg: p.neutral[100],
    "fg-2": p.neutral[300],
    // 4.79 on selected
    "fg-3": p.neutral[450],
    // 3.14 on disabled-fill
    "fg-disabled": p.neutral[550],
    "disabled-fill": p.neutral[750],
    "disabled-fg": p.neutral[550],
    "disabled-line": p.neutral[650],
    pressed: p.neutral[700],
    "pressed-line": p.neutral[500],
    "on-accent": p.white,
    "on-critical": p.white,
    accent: p.terracotta[500],
    "accent-hover": p.terracotta[600],
    "accent-pressed": p.terracotta[700],
    "accent-text": p.terracotta[300],
    "accent-soft": "rgb(242 154 128 / 0.3)",
    success: p.green[500],
    "success-soft": p.green[950],
    "success-line": p.green[800],
    warning: p.amber[500],
    "warning-soft": p.amber[950],
    "warning-line": p.amber[800],
    // 5.88 on raised, 4.96 on selected
    critical: p.crimson[300],
    "critical-soft": p.crimson[900],
    "critical-line": p.crimson[700],
    // white on it 7.25
    "critical-solid": p.crimson[500],
    "critical-solid-hover": p.crimson[600],
    info: p.neutral[300],
    "series-1": p.blue[500],
    "series-2": p.magenta[500],
    "series-3": p.violet[500],
    "series-4": p.aqua[500],
    "series-5": p.ochre[500],
    "series-prev": p.neutral[500],
    "series-focus": "rgb(57 135 229 / 0.25)",
    "series-partial": p.neutral[850],
    grid: p.neutral[750],
    axis: p.neutral[600],
    "shadow-soft": "rgb(0 0 0 / 0.32)",
    "shadow-strong": "rgb(0 0 0 / 0.5)",
  },
  light: {
    // on a light ground an input reads as white, set into the page
    sunken: p.paper[0],
    ground: p.paper[50],
    raised: p.paper[0],
    overlay: p.paper[0],
    hover: p.paper[150],
    selected: p.paper[200],
    // 1.45 on raised, 1.33 on ground
    muted: p.paper[275],
    scrim: "rgb(26 25 23 / 0.32)",
    line: p.paper[250],
    "line-strong": p.paper[300],
    "line-subtle": p.paper[150],
    // 3.64 on sunken, raised and overlay (white), 3.34 on ground
    "line-input": p.paper[500],
    fg: p.paper[900],
    "fg-2": p.paper[700],
    // 4.62 on selected
    "fg-3": p.paper[650],
    // 3.20 on disabled-fill
    "fg-disabled": p.paper[525],
    "disabled-fill": p.paper[150],
    "disabled-fg": p.paper[525],
    "disabled-line": p.paper[300],
    pressed: p.paper[200],
    "pressed-line": p.paper[400],
    "on-accent": p.white,
    "on-critical": p.white,
    accent: p.terracotta[500],
    "accent-hover": p.terracotta[600],
    "accent-pressed": p.terracotta[700],
    "accent-text": p.terracotta[600],
    "accent-soft": "rgb(181 72 46 / 0.22)",
    success: p.light.success,
    "success-soft": p.light.successSoft,
    "success-line": p.light.successLine,
    warning: p.light.warning,
    "warning-soft": p.light.warningSoft,
    "warning-line": p.light.warningLine,
    // 8.89 on white
    critical: p.crimson[600],
    "critical-soft": p.crimson.lightSoft,
    "critical-line": p.crimson.lightLine,
    "critical-solid": p.crimson[500],
    "critical-solid-hover": p.crimson[600],
    info: p.paper[700],
    "series-1": p.seriesLight[0],
    "series-2": p.seriesLight[1],
    "series-3": p.seriesLight[2],
    "series-4": p.seriesLight[3],
    "series-5": p.seriesLight[4],
    "series-prev": p.paper[400],
    "series-focus": "rgb(42 111 214 / 0.22)",
    "series-partial": p.paper[0],
    grid: p.paper[150],
    axis: p.paper[300],
    "shadow-soft": "rgb(26 25 23 / 0.1)",
    "shadow-strong": "rgb(26 25 23 / 0.18)",
  },
} as const satisfies Record<string, Roles>;

export type ThemeName = keyof typeof themes;
export const defaultTheme: ThemeName = "dark";

// css color-scheme of each theme, so native controls and scroll bars match
export const colorScheme: Record<ThemeName, "dark" | "light"> = { dark: "dark", light: "light" };

export const font = {
  sans: '"Inter Variable", ui-sans-serif, system-ui, sans-serif',
  mono: '"JetBrains Mono Variable", ui-monospace, "SF Mono", monospace',
} as const;

// the type scale: size / line height, weight, tracking. numbers are set in
// mono at small and metric only. `heading` is the console's page title;
// `page` stays for the website.
export const type = {
  axis: { size: "11px", line: "14px", weight: 400, tracking: "0" },
  caption: { size: "12px", line: "16px", weight: 500, tracking: "0.01em" },
  small: { size: "13px", line: "20px", weight: 400, tracking: "0" },
  body: { size: "14px", line: "20px", weight: 400, tracking: "0" },
  title: { size: "16px", line: "24px", weight: 600, tracking: "0" },
  section: { size: "20px", line: "28px", weight: 600, tracking: "-0.01em" },
  heading: { size: "22px", line: "28px", weight: 600, tracking: "-0.015em" },
  page: { size: "28px", line: "36px", weight: 600, tracking: "-0.025em" },
  metric: { size: "28px", line: "36px", weight: 500, tracking: "-0.02em" },
  h1: { size: "32px", line: "40px", weight: 600, tracking: "-0.03em" },
  display: { size: "48px", line: "52px", weight: 600, tracking: "-0.035em" },
} as const;

// the spacing scale, 4 px base. the console's tailwind spacing is generated
// from it (p-4 is 16 px) and check-usage refuses a padding, margin or gap
// outside it. rhythm: page gutter 6 (4 on a phone), gap between page
// sections 8, card padding 5 (4 on a phone), gap inside a card 3, form
// field gap 4.
export const space = {
  0: "0px",
  1: "4px",
  2: "8px",
  3: "12px",
  4: "16px",
  5: "20px",
  6: "24px",
  8: "32px",
  10: "40px",
  12: "48px",
  16: "64px",
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

// the geometry of each elevation; its colour is the theme's shadow role
export const shadow = {
  overlay: "0 8px 24px var(--vs-shadow-soft)",
  dialog: "0 16px 48px var(--vs-shadow-strong)",
} as const;

// motion. every motion carries a meaning (system v2):
//   press    a click registered
//   reveal   a menu, tooltip or popover opens from its trigger
//   change   a value moved, a dialog opens
//   arrival  something new arrived (first event, new alert)
//   highlight the fading mark left by an arrival
//   live     the breathing dot of anything listening
//   number   a value counting to its new number
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
    number: 400,
  },
  ease: {
    standard: [0.2, 0, 0, 1],
    exit: [0.4, 0, 1, 1],
  },
  // the 4 px a revealed or arriving element travels
  distance: 4,
  // lists reveal their children one after another: the gap between two
  // children and the wait before the first (ms)
  stagger: { children: 30, delay: 20 },
  // the spring of a layout move (the current nav item, a segmented marker)
  spring: { stiffness: 520, damping: 42, mass: 0.8 },
} as const;

// stacking layers above the page. a table header that sticks to the top
// sits over the rows (and their raised controls) and under the shell
// header. a toast sits over menus and the sticky header; a native <dialog>
// is in the top layer above all of them.
export const layer = {
  raised: 10,
  sticky: 20,
  header: 30,
  overlay: 50,
  toast: 60,
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
