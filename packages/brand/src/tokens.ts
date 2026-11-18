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
  // true neutrals (oklch chroma 0) for the dark theme, by oklch lightness x1000
  neutral: {
    145: "#0a0a0a",
    173: "#101010",
    209: "#181818",
    235: "#1e1e1e",
    244: "#202020",
    264: "#252525",
    277: "#282828",
    289: "#2b2b2b",
    371: "#404040",
    530: "#6c6c6c",
    540: "#6f6f6f",
    660: "#929292",
    770: "#b4b4b4",
    850: "#cecece",
    900: "#dedede",
    949: "#eeeeee",
  },
  // true neutrals for the light theme, by oklch lightness x1000
  paper: {
    1000: "#ffffff",
    976: "#f7f7f7",
    955: "#f0f0f0",
    934: "#e9e9e9",
    904: "#dfdfdf",
    901: "#dedede",
    802: "#bebebe",
    640: "#8c8c8c",
    630: "#898989",
    620: "#868686",
    521: "#696969",
    439: "#525252",
    360: "#3d3d3d",
    300: "#2e2e2e",
    205: "#171717",
  },
  white: "#ffffff",
  // status, the quiet rule: one oklch lightness and chroma per theme and
  // role, only the hue moves (success 150, warning 75, critical 25, info
  // 250). text is the word and the dot, line the thin edge
  dark: {
    success: "#90c79b",
    successLine: "#376040",
    warning: "#d8b079",
    warningLine: "#6c4f25",
    critical: "#e9a29c",
    criticalLine: "#784541",
    info: "#8ebcec",
    infoLine: "#36587b",
    criticalSolid: "#c13c3b",
    criticalSolidHover: "#aa3333",
  },
  light: {
    success: "#337344",
    successLine: "#9ecba6",
    warning: "#845a0f",
    warningLine: "#d9b88b",
    critical: "#944a46",
    criticalLine: "#e8ada7",
    info: "#32669a",
    infoLine: "#9cc2ea",
    criticalSolid: "#ba2b2e",
    criticalSolidHover: "#a32226",
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
  // surface #ffffff, all at least 3:1)
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
  // the background of selected text
  selection: string;
  // status, always shown with a dot or an icon and a word. the colour
  // (dot, icon, word of a change) and the thin edge of a label or a notice;
  // a status never fills a surface, the word stays neutral
  success: string;
  "success-line": string;
  warning: string;
  "warning-line": string;
  critical: string;
  "critical-line": string;
  // the fill of the confirm button of a destructive dialog, and its hover;
  // a destructive action anywhere else is drawn in `critical` text with an
  // icon, never a fill
  "critical-solid": string;
  "critical-solid-hover": string;
  info: string;
  "info-line": string;
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
    sunken: p.neutral[145],
    ground: p.neutral[173],
    raised: p.neutral[209],
    overlay: p.neutral[235],
    hover: p.neutral[244],
    selected: p.neutral[264],
    // 1.25 on raised, 1.34 on ground
    muted: p.neutral[289],
    scrim: "rgb(0 0 0 / 0.6)",
    line: p.neutral[277],
    "line-strong": p.neutral[371],
    "line-subtle": p.neutral[235],
    // 3.94 on sunken, 3.53 on raised, 3.32 on overlay
    "line-input": p.neutral[540],
    fg: p.neutral[949],
    "fg-2": p.neutral[770],
    // 5.71 on raised, 4.93 on selected
    "fg-3": p.neutral[660],
    // 3.10 on disabled-fill
    "fg-disabled": p.neutral[530],
    "disabled-fill": p.neutral[244],
    "disabled-fg": p.neutral[530],
    "disabled-line": p.neutral[277],
    pressed: p.neutral[264],
    "pressed-line": p.neutral[540],
    // ink: the primary fill is the text colour, its text the ground (16.4)
    "on-accent": p.neutral[173],
    "on-critical": p.white,
    accent: p.neutral[949],
    "accent-hover": p.neutral[900],
    "accent-pressed": p.neutral[850],
    // links, markers and the focus ring: 15.3 on raised
    "accent-text": p.neutral[949],
    selection: "rgb(238 238 238 / 0.2)",
    success: p.dark.success,
    "success-line": p.dark.successLine,
    warning: p.dark.warning,
    "warning-line": p.dark.warningLine,
    critical: p.dark.critical,
    "critical-line": p.dark.criticalLine,
    // white on it 5.28
    "critical-solid": p.dark.criticalSolid,
    "critical-solid-hover": p.dark.criticalSolidHover,
    info: p.dark.info,
    "info-line": p.dark.infoLine,
    "series-1": p.blue[500],
    "series-2": p.magenta[500],
    "series-3": p.violet[500],
    "series-4": p.aqua[500],
    "series-5": p.ochre[500],
    "series-prev": p.neutral[540],
    "series-focus": "rgb(57 135 229 / 0.25)",
    "series-partial": p.neutral[209],
    grid: p.neutral[277],
    axis: p.neutral[371],
    "shadow-soft": "rgb(0 0 0 / 0.32)",
    "shadow-strong": "rgb(0 0 0 / 0.5)",
  },
  light: {
    sunken: p.paper[955],
    ground: p.paper[976],
    raised: p.paper[1000],
    overlay: p.paper[1000],
    hover: p.paper[955],
    selected: p.paper[934],
    // 1.35 on raised, 1.26 on ground
    muted: p.paper[901],
    scrim: "rgb(0 0 0 / 0.32)",
    line: p.paper[904],
    "line-strong": p.paper[802],
    "line-subtle": p.paper[934],
    // 3.19 on sunken, 3.40 on ground, 3.64 on raised and overlay
    "line-input": p.paper[620],
    fg: p.paper[205],
    "fg-2": p.paper[439],
    // 5.12 on ground, 4.52 on selected
    "fg-3": p.paper[521],
    // 3.07 on disabled-fill
    "fg-disabled": p.paper[630],
    "disabled-fill": p.paper[955],
    "disabled-fg": p.paper[630],
    "disabled-line": p.paper[802],
    pressed: p.paper[934],
    "pressed-line": p.paper[640],
    // ink: the primary fill is the text colour, white text on it (17.9)
    "on-accent": p.white,
    "on-critical": p.white,
    accent: p.paper[205],
    "accent-hover": p.paper[300],
    "accent-pressed": p.paper[360],
    "accent-text": p.paper[205],
    selection: "rgb(23 23 23 / 0.14)",
    success: p.light.success,
    "success-line": p.light.successLine,
    warning: p.light.warning,
    "warning-line": p.light.warningLine,
    critical: p.light.critical,
    "critical-line": p.light.criticalLine,
    // white on it 6.05
    "critical-solid": p.light.criticalSolid,
    "critical-solid-hover": p.light.criticalSolidHover,
    info: p.light.info,
    "info-line": p.light.infoLine,
    "series-1": p.seriesLight[0],
    "series-2": p.seriesLight[1],
    "series-3": p.seriesLight[2],
    "series-4": p.seriesLight[3],
    "series-5": p.seriesLight[4],
    "series-prev": p.paper[640],
    "series-focus": "rgb(42 111 214 / 0.22)",
    "series-partial": p.paper[1000],
    grid: p.paper[934],
    axis: p.paper[802],
    "shadow-soft": "rgb(0 0 0 / 0.1)",
    "shadow-strong": "rgb(0 0 0 / 0.18)",
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
// the sans with tabular figures; mono is for code, ids, keys and routes.
// `heading` is the console's page title; `page` stays for the website.
export const type = {
  axis: { size: "11px", line: "14px", weight: 400, tracking: "0" },
  caption: { size: "12px", line: "16px", weight: 500, tracking: "0.01em" },
  small: { size: "13px", line: "20px", weight: 400, tracking: "0" },
  // a status label, pill or badge: the word that carries a state
  label: { size: "13px", line: "16px", weight: 500, tracking: "0" },
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
