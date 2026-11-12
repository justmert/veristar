// the console's mark: a star on an orbit, drawn inline so it needs no
// image request and follows the text colour
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true" fill="none">
      <ellipse
        cx="16"
        cy="16"
        rx="13"
        ry="6.5"
        transform="rotate(-24 16 16)"
        stroke="currentColor"
        strokeOpacity="0.45"
        strokeWidth="1.6"
      />
      <path
        d="M16 7.5l2.1 6.4 6.4 2.1-6.4 2.1L16 24.5l-2.1-6.4L7.5 16l6.4-2.1z"
        fill="currentColor"
      />
    </svg>
  );
}
