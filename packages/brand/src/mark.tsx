import type { SVGProps } from "react";

// the veristar mark: an orbit circle crossed by a meridian, with the accent
// star at its top. drawn in the current text colour; the star takes the
// accent. the same geometry as docs/logo/veristar.svg and the umami favicon.
export const MARK_STAR =
  "M16 1.5 L17.3 4.7 L20.5 6 L17.3 7.3 L16 10.5 L14.7 7.3 L11.5 6 L14.7 4.7 Z";

export function Mark({
  title,
  mono = false,
  ...props
}: SVGProps<SVGSVGElement> & { title?: string; mono?: boolean }) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="none"
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
      {...props}
    >
      <circle cx="16" cy="17" r="11" stroke="currentColor" strokeWidth="2.4" />
      <path d="M16 6v22" stroke="currentColor" strokeWidth="2.4" />
      <path d={MARK_STAR} className={mono ? "fill-current" : "fill-accent"} />
    </svg>
  );
}
