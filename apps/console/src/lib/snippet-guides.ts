import type { Snippet } from "@/api/schemas";

export type Guide = {
  id: string;
  label: string;
  where: string;
  note: string;
  code: string;
  // the text the copy button copies: the bare tag, or the npm command
  copy: string;
};

// consent mode: with data-auto-track="false" the tracker loads but sends
// nothing until the site calls umami.start() (docs/onboarding/web/consent.md).
// the attribute is outside what the integrity hash covers, so the pinned
// version and hash stay exactly as the control plane built them.
export const CONSENT_ATTR = 'data-auto-track="false"';
export const CONSENT_START = "window.umami?.start();";

export function consentTag(tag: string): string {
  return tag.replace(/><\/script>$/, ` ${CONSENT_ATTR}></script>`);
}

// from tracker 1.3.0 the page title is sent only when the tag asks for it.
// older releases always send it, so the choice exists only from that version
export const TITLE_ATTR = 'data-send-title="true"';
export const SEND_TITLE_SINCE = "1.3.0";

export function titleTag(tag: string): string {
  return tag.replace(/><\/script>$/, ` ${TITLE_ATTR}></script>`);
}

// whether a release reads data-send-title
export function readsTitleChoice(version: string): boolean {
  const a = version.split(".").map(Number);
  const b = SEND_TITLE_SINCE.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if ((a[i] ?? 0) !== (b[i] ?? 0)) return (a[i] ?? 0) > (b[i] ?? 0);
  }
  return true;
}

// the package's init() call: the tag's attributes in camel case
// (docs/onboarding/web/npm.md). the gateway origin is the one the tag loads
// the tracker from.
export function npmInit(s: Snippet, opts: { consent?: boolean; title?: boolean } = {}): string {
  const parts = [`siteId: "${s.site_id}"`, `host: "${new URL(s.src).origin}"`];
  if (opts.consent) parts.push("autoTrack: false");
  if (opts.title && readsTitleChoice(s.npm?.version ?? s.version)) parts.push("sendTitle: true");
  return `init({ ${parts.join(", ")} });`;
}

// every guide carries the tag exactly as the control plane built it (contract
// 4.3.6), or with the consent and title attributes added when asked; only the
// surrounding file context differs per framework
export function snippetGuides(
  s: Snippet,
  opts: { consent?: boolean; title?: boolean } = {},
): Guide[] {
  let tag = opts.consent ? consentTag(s.tag) : s.tag;
  if (opts.title && readsTitleChoice(s.version)) tag = titleTag(tag);
  const inHead = (before: string[], after: string[]) =>
    [...before, `  ${tag}`, ...after].join("\n");
  const guides: Guide[] = [
    {
      id: "html",
      label: "Static HTML",
      where: "The <head> of every page",
      note: "Paste the tag once per page. Pages without it are not counted.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: tag,
    },
    {
      id: "react",
      label: "React",
      where: "index.html (Vite) or public/index.html (Create React App)",
      note: "Put it in the HTML file, not in a component, so it loads once. The tracker counts client-side route changes on its own.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: tag,
    },
    {
      id: "vue",
      label: "Vue",
      where: "index.html at the root of the Vite project",
      note: "Put it in the HTML file, not in a component. Vue Router navigation is counted by the tracker on its own.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: tag,
    },
    {
      id: "astro",
      label: "Astro",
      where: "The layout component every page uses, for example src/layouts/Layout.astro",
      note: "Astro leaves a script tag with attributes other than src unprocessed, so the tag is sent to the browser exactly as written.",
      code: inHead(
        ["---", "const { title } = Astro.props;", "---", "<head>", "  <title>{title}</title>"],
        ["</head>"],
      ),
      copy: tag,
    },
    {
      id: "sveltekit",
      label: "SvelteKit",
      where: "src/app.html",
      note: "Put it before %sveltekit.head%. Client-side navigation is counted by the tracker on its own.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["  %sveltekit.head%", "</head>"]),
      copy: tag,
    },
    {
      id: "ssr",
      label: "Server-rendered",
      where: "The base template your server renders every page with",
      note: "For example a layout in Express, Django, Rails, Laravel or Go templates. Write the tag as plain HTML, outside any escaping helper.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: tag,
    },
  ];
  if (s.npm) {
    guides.push({
      id: "npm",
      label: "npm",
      where: `Package ${s.npm.package} ${s.npm.version}`,
      note: "Install the pinned version, then import it and call init() once in your app's entry module.",
      code: `${s.npm.install}\n\n${s.npm.import}\n${npmInit(s, opts)}`,
      copy: s.npm.install,
    });
  }
  return guides;
}
