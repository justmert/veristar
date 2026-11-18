import { Fragment } from "react";

// a domain, origin or dns name that wraps at its dots and slashes first and
// mid-word only when one part is longer than the line
export function Breakable({ text }: { text: string }) {
  const parts = text.split(/(?<=[./])/);
  return (
    <span className="wrap-anywhere">
      {parts.map((p, i) => (
        // parts have no identity beyond their position in the text
        <Fragment key={i}>
          {p}
          {i < parts.length - 1 ? <wbr /> : null}
        </Fragment>
      ))}
    </span>
  );
}
