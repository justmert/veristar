// a small markdown renderer for the terms document (contract 4.1.5). it
// builds react elements, never html strings, so nothing in the text can
// inject markup. it covers what docs/compliance/terms.md uses: headings,
// paragraphs, lists, block quotes, bold, inline code and links.

import type { ReactNode } from "react";

type Block =
  | { kind: "h"; level: number; text: string }
  | { kind: "p"; text: string }
  | { kind: "ul" | "ol"; items: string[] }
  | { kind: "quote"; text: string };

export function stripFrontMatter(md: string): string {
  const m = /^---\r?\n[\s\S]*?\r?\n---\r?\n?/.exec(md);
  return m ? md.slice(m[0].length) : md;
}

export function parseBlocks(md: string): Block[] {
  const blocks: Block[] = [];
  const lines = stripFrontMatter(md).split(/\r?\n/);
  let para: string[] = [];
  let list: { kind: "ul" | "ol"; items: string[] } | null = null;
  let quote: string[] = [];

  const flush = () => {
    if (para.length) blocks.push({ kind: "p", text: para.join(" ") });
    if (list) blocks.push(list);
    if (quote.length) blocks.push({ kind: "quote", text: quote.join(" ") });
    para = [];
    list = null;
    quote = [];
  };

  for (const raw of lines) {
    const line = raw.trimEnd();
    const heading = /^(#{1,4})\s+(.*)$/.exec(line);
    const bullet = /^\s*[-*]\s+(.*)$/.exec(line);
    const numbered = /^\s*\d+\.\s+(.*)$/.exec(line);
    const q = /^>\s?(.*)$/.exec(line);
    if (line.trim() === "") {
      flush();
    } else if (heading?.[1] && heading[2] !== undefined) {
      flush();
      blocks.push({ kind: "h", level: heading[1].length, text: heading[2] });
    } else if (bullet?.[1] !== undefined || numbered?.[1] !== undefined) {
      const kind = bullet ? "ul" : "ol";
      const text = (bullet?.[1] ?? numbered?.[1]) as string;
      if (para.length || quote.length || (list && list.kind !== kind)) flush();
      if (!list) list = { kind, items: [] };
      list.items.push(text);
    } else if (q?.[1] !== undefined) {
      if (para.length || list) flush();
      quote.push(q[1]);
    } else if (list && /^\s{2,}\S/.test(raw)) {
      // an indented continuation line belongs to the last list item
      const last = list.items.length - 1;
      list.items[last] = `${list.items[last]} ${line.trim()}`;
    } else {
      if (list || quote.length) flush();
      para.push(line.trim());
    }
  }
  flush();
  return blocks;
}

const INLINE = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)\s]+\))/g;

function safeHref(href: string): string | null {
  if (href.startsWith("/") && !href.startsWith("//")) return href;
  try {
    const u = new URL(href);
    return u.protocol === "https:" || u.protocol === "mailto:" ? u.href : null;
  } catch {
    return null;
  }
}

export function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let i = 0;
  for (const m of text.matchAll(INLINE)) {
    const token = m[0];
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const key = `i${i++}`;
    if (token.startsWith("**")) {
      out.push(<strong key={key}>{token.slice(2, -2)}</strong>);
    } else if (token.startsWith("`")) {
      out.push(
        <code key={key} className="rounded bg-muted px-1 font-mono text-[0.9em]">
          {token.slice(1, -1)}
        </code>,
      );
    } else {
      const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(token);
      const href = link?.[2] ? safeHref(link[2]) : null;
      if (link?.[1] && href) {
        out.push(
          <a key={key} href={href} className="text-primary underline underline-offset-2">
            {link[1]}
          </a>,
        );
      } else {
        out.push(link?.[1] ?? token);
      }
    }
    last = at + token.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export function Markdown({ source }: { source: string }) {
  const blocks = parseBlocks(source);
  return (
    <div className="space-y-4 leading-relaxed">
      {blocks.map((b, n) => {
        // blocks have no identity beyond their position in a static document
        const key = `b${n}`;
        switch (b.kind) {
          case "h": {
            const cls =
              b.level === 1
                ? "text-3xl font-semibold tracking-tight"
                : b.level === 2
                  ? "pt-4 text-xl font-semibold tracking-tight"
                  : "pt-2 text-base font-semibold";
            if (b.level === 1)
              return (
                <h1 key={key} className={cls}>
                  {inline(b.text)}
                </h1>
              );
            if (b.level === 2)
              return (
                <h2 key={key} className={cls}>
                  {inline(b.text)}
                </h2>
              );
            return (
              <h3 key={key} className={cls}>
                {inline(b.text)}
              </h3>
            );
          }
          case "p":
            return <p key={key}>{inline(b.text)}</p>;
          case "quote":
            return (
              <blockquote
                key={key}
                className="border-l-4 border-warn bg-warn/8 py-2 pr-3 pl-4 text-sm"
              >
                {inline(b.text)}
              </blockquote>
            );
          case "ul":
            return (
              <ul key={key} className="list-disc space-y-1 pl-6">
                {b.items.map((it, j) => (
                  <li key={`${key}-${j}`}>{inline(it)}</li>
                ))}
              </ul>
            );
          case "ol":
            return (
              <ol key={key} className="list-decimal space-y-1 pl-6">
                {b.items.map((it, j) => (
                  <li key={`${key}-${j}`}>{inline(it)}</li>
                ))}
              </ol>
            );
        }
        return null;
      })}
    </div>
  );
}
