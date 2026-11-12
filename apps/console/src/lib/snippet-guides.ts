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

// every guide carries the tag exactly as the control plane built it (contract
// 4.3.6); only the surrounding file context differs per framework
export function snippetGuides(s: Snippet): Guide[] {
  const inHead = (before: string[], after: string[]) =>
    [...before, `  ${s.tag}`, ...after].join("\n");
  const guides: Guide[] = [
    {
      id: "html",
      label: "Static HTML",
      where: "The <head> of every page",
      note: "Paste the tag once per page. Pages without it are not counted.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: s.tag,
    },
    {
      id: "react",
      label: "React",
      where: "index.html (Vite) or public/index.html (Create React App)",
      note: "Put it in the HTML file, not in a component, so it loads once. The tracker counts client-side route changes on its own.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: s.tag,
    },
    {
      id: "vue",
      label: "Vue",
      where: "index.html at the root of the Vite project",
      note: "Put it in the HTML file, not in a component. Vue Router navigation is counted by the tracker on its own.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: s.tag,
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
      copy: s.tag,
    },
    {
      id: "sveltekit",
      label: "SvelteKit",
      where: "src/app.html",
      note: "Put it before %sveltekit.head%. Client-side navigation is counted by the tracker on its own.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["  %sveltekit.head%", "</head>"]),
      copy: s.tag,
    },
    {
      id: "ssr",
      label: "Server-rendered",
      where: "The base template your server renders every page with",
      note: "For example a layout in Express, Django, Rails, Laravel or Go templates. Write the tag as plain HTML, outside any escaping helper.",
      code: inHead(["<head>", '  <meta charset="utf-8" />'], ["</head>"]),
      copy: s.tag,
    },
  ];
  if (s.npm) {
    guides.push({
      id: "npm",
      label: "npm",
      where: `Package ${s.npm.package} ${s.npm.version}`,
      note: "Install the pinned version, then import it once in your app's entry module.",
      code: `${s.npm.install}\n\n${s.npm.import}`,
      copy: s.npm.install,
    });
  }
  return guides;
}
