// a small markdown renderer for the terms document (contract 4.1.5). it
// builds react elements, never html strings, so nothing in the text can
// inject markup. it covers what docs/compliance/terms.md uses: headings,
// paragraphs, lists with one nested level, block quotes, bold, inline code,
// links, bare https addresses and pipe tables.

import type { ReactNode } from "react";

// a list item with the items nested one level under it
type ListItem = { text: string; sub: string[] };

type Block =
  | { kind: "h"; level: number; text: string }
  | { kind: "p"; text: string }
  | { kind: "ul" | "ol"; items: ListItem[] }
  | { kind: "quote"; text: string }
  | { kind: "table"; head: string[]; rows: string[][] };

// the cells of a pipe table row: "| a | b |" is ["a", "b"]
function cells(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
}
const TABLE_RULE = /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/;

export function stripFrontMatter(md: string): string {
  const m = /^---\r?\n[\s\S]*?\r?\n---\r?\n?/.exec(md);
  return m ? md.slice(m[0].length) : md;
}

export function parseBlocks(md: string): Block[] {
  const blocks: Block[] = [];
  const lines = stripFrontMatter(md).split(/\r?\n/);
  let para: string[] = [];
  let list: { kind: "ul" | "ol"; items: ListItem[] } | null = null;
  let quote: string[] = [];
  let table: { kind: "table"; head: string[]; rows: string[][] } | null = null;

  const flush = () => {
    if (table) blocks.push(table);
    table = null;
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
    const bullet = /^(\s*)[-*]\s+(.*)$/.exec(line);
    const numbered = /^(\s*)\d+\.\s+(.*)$/.exec(line);
    const indent = (bullet?.[1] ?? numbered?.[1] ?? "").length;
    const lastItem = list?.items.at(-1);
    const q = /^>\s?(.*)$/.exec(line);
    if (table && line.trim().startsWith("|")) {
      table.rows.push(cells(line));
    } else if (line.trim() === "") {
      flush();
    } else if (
      para.length === 1 &&
      !list &&
      para[0]?.startsWith("|") &&
      TABLE_RULE.test(line)
    ) {
      // the line before the rule was the header row of a table
      const head = cells(para[0]);
      para = [];
      flush();
      table = { kind: "table", head, rows: [] };
    } else if (heading?.[1] && heading[2] !== undefined) {
      flush();
      blocks.push({ kind: "h", level: heading[1].length, text: heading[2] });
    } else if (indent >= 2 && lastItem && (bullet || numbered)) {
      // an indented item nests under the last item of the list
      lastItem.sub.push((bullet?.[2] ?? numbered?.[2]) as string);
    } else if (bullet?.[2] !== undefined || numbered?.[2] !== undefined) {
      const kind = bullet ? "ul" : "ol";
      const text = (bullet?.[2] ?? numbered?.[2]) as string;
      if (para.length || quote.length || (list && list.kind !== kind)) flush();
      if (!list) list = { kind, items: [] };
      list.items.push({ text, sub: [] });
    } else if (q?.[1] !== undefined) {
      if (para.length || list) flush();
      quote.push(q[1]);
    } else if (lastItem && /^\s{2,}\S/.test(raw)) {
      // an indented continuation line belongs to the last list item, or to
      // its last nested item when it is indented that far
      const deep = /^\s{4,}\S/.test(raw) && lastItem.sub.length > 0;
      if (deep) {
        const j = lastItem.sub.length - 1;
        lastItem.sub[j] = `${lastItem.sub[j]} ${line.trim()}`;
      } else lastItem.text = `${lastItem.text} ${line.trim()}`;
    } else {
      if (list || quote.length) flush();
      para.push(line.trim());
    }
  }
  flush();
  return blocks;
}

const INLINE = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)\s]+\)|https:\/\/[^\s)<>"]+[^\s)<>".,;:!?])/g;

function safeHref(href: string): string | null {
  if (href.startsWith("/") && !href.startsWith("//")) return href;
  try {
    const u = new URL(href);
    return u.protocol === "https:" || u.protocol === "mailto:" ? u.href : null;
  } catch {
    return null;
  }
}

const LINK =
  "rounded-sm break-words text-fg underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:decoration-fg focus-ring";

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
      out.push(
        <strong key={key} className="font-semibold text-fg">
          {token.slice(2, -2)}
        </strong>,
      );
    } else if (token.startsWith("`")) {
      out.push(
        <code key={key} className="rounded-sm bg-sunken px-1 font-mono text-small">
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("https://")) {
      const href = safeHref(token);
      out.push(
        href ? (
          <a key={key} href={href} className={LINK}>
            {token}
          </a>
        ) : (
          token
        ),
      );
    } else {
      const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(token);
      const href = link?.[2] ? safeHref(link[2]) : null;
      if (link?.[1] && href) {
        out.push(
          <a key={key} href={href} className={LINK}>
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

// an anchor id for a heading, so a table of contents can link to it
export function headingId(text: string): string {
  return (
    text
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "") || "section"
  );
}

// the second-level headings of a document, in order, for a table of contents
export function outline(source: string): { id: string; text: string }[] {
  return parseBlocks(source)
    .filter((b): b is Extract<Block, { kind: "h" }> => b.kind === "h" && b.level === 2)
    .map((b) => ({
      id: headingId(b.text.replace(/[*`]/g, "")),
      text: b.text.replace(/[*`]/g, ""),
    }));
}

// "1. Parties and roles" is the number "1" and the title "Parties and
// roles"; a heading without a number has none
export function splitNumber(text: string): { n: string | null; title: string } {
  const m = /^(\d+(?:\.\d+)*)\.?\s+(.*)$/.exec(text);
  return m?.[1] && m[2] !== undefined ? { n: m[1], title: m[2] } : { n: null, title: text };
}

// `afterTitle` is placed right under the document's first-level heading (the
// terms page puts the version there)
export function Markdown({ source, afterTitle }: { source: string; afterTitle?: ReactNode }) {
  const blocks = parseBlocks(source);
  const titled = blocks.some((b) => b.kind === "h" && b.level === 1);
  return (
    <div className="flex flex-col gap-4.5 text-[15px] leading-[25px] text-fg-2">
      {titled ? null : afterTitle}
      {blocks.map((b, n) => {
        // blocks have no identity beyond their position in a static document
        const key = `b${n}`;
        switch (b.kind) {
          case "h": {
            if (b.level === 1)
              return (
                <div key={key} className="flex flex-col gap-3">
                  <h1 className="text-[34px] leading-10 font-semibold tracking-[-0.03em] text-fg">
                    {inline(b.text)}
                  </h1>
                  {afterTitle}
                </div>
              );
            const { n, title } = splitNumber(b.text);
            if (b.level === 2)
              return (
                <h2
                  key={key}
                  id={headingId(b.text.replace(/[*`]/g, ""))}
                  className="mt-3.5 flex scroll-mt-6 gap-3 text-[20px] leading-7 font-semibold tracking-[-0.015em] text-fg"
                >
                  {n ? <span className="text-fg-3 tabular">{n}</span> : null}
                  <span>{inline(title)}</span>
                </h2>
              );
            return (
              <h3 key={key} className="mt-1 flex gap-2.5 text-[16px] leading-6 font-semibold text-fg">
                {n ? <span className="text-fg-3 tabular">{n}</span> : null}
                <span>{inline(title)}</span>
              </h3>
            );
          }
          case "p":
            return <p key={key}>{inline(b.text)}</p>;
          case "table":
            return (
              <div key={key} className="overflow-x-auto rounded-xl border border-line">
                <table className="w-full border-collapse text-left text-[13.5px] leading-5">
                  <thead className="bg-sunken text-caption text-fg-2">
                    <tr>
                      {b.head.map((h, j) => (
                        <th key={`${key}-h${j}`} scope="col" className="px-4.5 py-2.5 font-normal">
                          {inline(h)}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {b.rows.map((r, j) => (
                      <tr key={`${key}-r${j}`} className="border-t border-line-subtle">
                        {r.map((c, k) => (
                          <td
                            key={`${key}-r${j}-${k}`}
                            className={k === 0 ? "px-4.5 py-2.5 text-fg-2" : "px-4.5 py-2.5 text-fg"}
                          >
                            {inline(c)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          case "quote":
            return (
              <blockquote
                key={key}
                className="rounded-md border border-line bg-sunken px-3 py-2 text-small text-fg-2"
              >
                {inline(b.text)}
              </blockquote>
            );
          case "ul":
            return (
              <ul key={key} className="flex list-disc flex-col gap-2 pl-5 marker:text-fg-3">
                {b.items.map((it, j) => (
                  <Item key={`${key}-${j}`} item={it} />
                ))}
              </ul>
            );
          case "ol":
            return (
              <ol key={key} className="flex list-decimal flex-col gap-2 pl-5 marker:text-fg-3">
                {b.items.map((it, j) => (
                  <Item key={`${key}-${j}`} item={it} />
                ))}
              </ol>
            );
        }
        return null;
      })}
    </div>
  );
}

function Item({ item }: { item: ListItem }) {
  return (
    <li>
      {inline(item.text)}
      {item.sub.length > 0 ? (
        <ul className="mt-1 flex list-disc flex-col gap-1 pl-5 marker:text-fg-3">
          {item.sub.map((t, k) => (
            // nested items have no identity beyond their position
            <li key={k}>{inline(t)}</li>
          ))}
        </ul>
      ) : null}
    </li>
  );
}
