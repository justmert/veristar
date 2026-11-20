import { type SVGProps, useId } from "react";

// the veristar mark: a soft five point star turned over, its two upper arms
// run long into the v of the name, and a measured point held in a round cut
// between them. one colour, the current text colour. the same geometry as
// the svg files that scripts/generate.ts writes (favicons, docs logos).
export const MARK_POINTS = "24.00,40.00 20.47,33.35 13.06,32.05 18.29,26.65 9.01,7.87 24.00,22.50 38.99,7.87 29.71,26.65 34.94,32.05 27.53,33.35";
export const MARK_CUT = { cx: 24, cy: 16.5, r: 7.5 } as const;
export const MARK_DOT = { cx: 24, cy: 16.5, r: 3.4 } as const;

export function Mark({
  title,
  // kept for callers that ask for a one colour mark: the mark is one colour
  mono: _mono = false,
  ...props
}: SVGProps<SVGSVGElement> & { title?: string; mono?: boolean }) {
  // a mask id per instance: two marks on one page must not share one
  const cut = `vs-mark-cut-${useId().replace(/:/g, "")}`;
  return (
    <svg
      viewBox="0 0 48 48"
      fill="none"
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
      {...props}
    >
      <defs>
        <mask id={cut} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
          <rect width="48" height="48" fill="#fff" />
          <circle cx={MARK_CUT.cx} cy={MARK_CUT.cy} r={MARK_CUT.r} fill="#000" />
        </mask>
      </defs>
      <polygon
        points={MARK_POINTS}
        mask={`url(#${cut})`}
        fill="currentColor"
        stroke="currentColor"
        strokeWidth="4"
        strokeLinejoin="round"
      />
      <circle cx={MARK_DOT.cx} cy={MARK_DOT.cy} r={MARK_DOT.r} fill="currentColor" />
    </svg>
  );
}
