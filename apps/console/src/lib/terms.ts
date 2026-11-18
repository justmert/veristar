import { formatDate } from "@/lib/format";

// a terms version reads as its date: "2026-11-18.2" and
// "2026-11-18-draft.6" both read "Version of Nov 18, 2026". the full
// version stays in a tooltip, and a draft says so in its own word.
export function termsVersionLabel(version: string): string {
  const day = /^(\d{4}-\d{2}-\d{2})/.exec(version)?.[1];
  return day ? `Version of ${formatDate(day)}` : `Version ${version}`;
}

export function isDraftTerms(version: string, status?: string): boolean {
  return status === "draft" || version.includes("-draft");
}
