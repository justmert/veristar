import { type BadgeSubject, Pill, statusEntry } from "@/components/status";

// one badge set for every status the console shows (project, site, key,
// alert, grade): a dot and a word, never colour alone. the words live in
// status.tsx so text and badges say the same.

export function StatusBadge({
  className,
  live,
  ...subject
}: BadgeSubject & { className?: string; live?: boolean }) {
  const e = statusEntry(subject as BadgeSubject);
  const grade = subject.kind === "grade" ? (subject.grade ?? "none") : undefined;
  return (
    <Pill tone={e.tone} live={live} className={className}>
      <span data-status={subject.kind} data-grade={grade}>
        {e.word}
      </span>
    </Pill>
  );
}
