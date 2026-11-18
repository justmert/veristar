import { cn } from "@veristar/brand/cn";
import { type FormEvent, useEffect, useState } from "react";
import { TextField } from "@/components/form-field";
import { Segmented } from "@/components/segmented";
import { Button } from "@/components/ui/button";
import { formatDateRange, formatTimeRange } from "@/lib/format";

// ------------------------------------------------------------- date field

const DAY_RE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DAY_TIME_RE = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})$/;

export const DAY_MESSAGE = "Enter a day as 2026-11-18.";
export const DAY_TIME_MESSAGE = "Enter a day and time as 2026-11-18 14:00.";

// a real calendar day written as 2026-11-18 (2026-02-30 is not one)
export function isIsoDay(v: string): boolean {
  const m = DAY_RE.exec(v);
  if (!m) return false;
  const d = new Date(`${v}T00:00:00Z`);
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === v;
}

// a day and a utc time written as 2026-11-18 14:00, as an iso instant;
// null when it is not one
export function parseUtcDayTime(v: string): string | null {
  const m = DAY_TIME_RE.exec(v.trim());
  if (!m) return null;
  const [, y, mo, d, h, mi] = m;
  if (!isIsoDay(`${y}-${mo}-${d}`) || Number(h) > 23 || Number(mi) > 59) return null;
  return `${y}-${mo}-${d}T${h}:${mi}:00Z`;
}

// one date input for the whole console: a text field that shows and takes
// 2026-11-18, never the browser's own date picker with its local, often us,
// format. every day is a utc day. `time` also takes a utc time of day.
export function DateField({
  label,
  value,
  onChange,
  error,
  hint,
  time = false,
  optional,
  disabled,
  name,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  // a range or server error; the field's own format check comes first
  error?: string | null;
  hint?: string;
  time?: boolean;
  optional?: boolean;
  disabled?: boolean;
  name?: string;
}) {
  const [touched, setTouched] = useState(false);
  const v = value.trim();
  const valid = v === "" || (time ? parseUtcDayTime(v) !== null : isIsoDay(v));
  const own = touched && !valid ? (time ? DAY_TIME_MESSAGE : DAY_MESSAGE) : null;
  return (
    <TextField
      label={label}
      name={name}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onBlur={() => setTouched(true)}
      hint={hint ?? (time ? "UTC, as 2026-11-18 14:00" : "UTC day, as 2026-11-18")}
      error={own ?? error ?? null}
      optional={optional}
      disabled={disabled}
      placeholder={time ? "2026-11-18 14:00" : "2026-11-18"}
      // a text keyboard: the numeric pad of a phone has no dash. the
      // pattern tells assistive technology the shape
      inputMode="text"
      pattern={time ? "\\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}" : "\\d{4}-\\d{2}-\\d{2}"}
      autoComplete="off"
      spellCheck={false}
      className="font-mono tabular max-lg:h-(--vs-control-touch)"
    />
  );
}

// ---------------------------------------------------------- range control

export type CustomRange = { from: string; to: string };

const DAY_MS = 86_400_000;

// the checks every range shares; a data source adds its own limits
export function validateRange(
  r: CustomRange,
  opts: { time?: boolean; maxDays?: number; minDay?: string; maxDay?: string } = {},
): { from?: string; to?: string } {
  const out: { from?: string; to?: string } = {};
  const parse = (v: string) =>
    opts.time ? parseUtcDayTime(v) : isIsoDay(v.trim()) ? `${v.trim()}T00:00:00Z` : null;
  const message = opts.time ? DAY_TIME_MESSAGE : DAY_MESSAGE;
  const a = parse(r.from);
  const b = parse(r.to);
  if (!a) out.from = message;
  if (!b) out.to = message;
  if (!a || !b) return out;
  const day = (iso: string) => iso.slice(0, 10);
  if (opts.minDay && day(a) < opts.minDay) out.from = `Start on or after ${opts.minDay}.`;
  if (opts.maxDay && day(b) > opts.maxDay) out.to = `End on or before ${opts.maxDay}.`;
  if (opts.time ? b <= a : b < a)
    out.to = opts.time ? "End after the start." : "End on or after the start.";
  else if (opts.maxDays !== undefined) {
    const days = Math.floor((Date.parse(b) - Date.parse(a)) / DAY_MS) + (opts.time ? 0 : 1);
    if (days > opts.maxDays) out.to = `A range covers at most ${opts.maxDays} days.`;
  }
  return out;
}

// one range control for every data source: presets passed in, plus a
// custom range of utc days (or utc times). the chosen range is always said
// in words with "UTC".
export function RangeControl<T extends string>({
  label = "Range",
  value,
  presets,
  onPreset,
  custom,
  onCustom,
  range,
  time = false,
  maxDays,
  minDay,
  maxDay,
  validate,
  className,
}: {
  label?: string;
  // the current preset, or "custom" while a custom range is applied
  value: T | "custom";
  presets: readonly { value: T; label: string }[];
  onPreset: (value: T) => void;
  // the applied custom range, which fills the form when it opens
  custom?: CustomRange | null;
  // without it the control offers presets only
  onCustom?: (range: CustomRange) => void;
  // the resolved range the page shows, said in words
  range?: CustomRange | null;
  time?: boolean;
  maxDays?: number;
  minDay?: string;
  maxDay?: string;
  // a data source's own rules, after the shared ones
  validate?: (r: CustomRange) => { from?: string; to?: string };
  className?: string;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<CustomRange>(custom ?? { from: "", to: "" });
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});

  // an applied range from the address fills the form again
  useEffect(() => {
    if (custom) setDraft(custom);
  }, [custom]);

  const options: { value: T | "custom"; label: string }[] = [
    ...presets,
    ...(onCustom ? [{ value: "custom" as const, label: "Custom" }] : []),
  ];
  const shown = editing ? "custom" : value;

  function choose(v: T | "custom") {
    setErrors({});
    if (v === "custom") {
      setEditing(true);
      return;
    }
    setEditing(false);
    onPreset(v);
  }

  function apply(e: FormEvent) {
    e.preventDefault();
    const shared = validateRange(draft, { time, maxDays, minDay, maxDay });
    const own = Object.keys(shared).length ? {} : (validate?.(draft) ?? {});
    const all = { ...own, ...shared };
    setErrors(all);
    if (all.from || all.to) return;
    setEditing(false);
    const trim = { from: draft.from.trim(), to: draft.to.trim() };
    onCustom?.(
      time
        ? { from: parseUtcDayTime(trim.from) ?? trim.from, to: parseUtcDayTime(trim.to) ?? trim.to }
        : trim,
    );
  }

  const words = range
    ? time
      ? formatTimeRange(range.from, range.to)
      : `${formatDateRange(range.from, range.to)}, UTC`
    : null;

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Segmented label={label} value={shown} options={options} onChange={choose} />
        {words ? (
          <span className="text-small text-fg-2" aria-live="polite">
            {words}
          </span>
        ) : (
          <span className="text-caption text-fg-2">UTC</span>
        )}
      </div>
      {editing ? (
        <form
          aria-label="Custom range"
          noValidate
          onSubmit={apply}
          className="flex animate-rise-in flex-wrap items-start gap-4 rounded-lg border border-line bg-raised p-4"
        >
          <div className="w-full sm:w-48">
            <DateField
              label={time ? "From (UTC)" : "From (UTC day)"}
              time={time}
              value={draft.from}
              onChange={(from) => setDraft((d) => ({ ...d, from }))}
              error={errors.from}
            />
          </div>
          <div className="w-full sm:w-48">
            <DateField
              label={time ? "To (UTC)" : "To (UTC day)"}
              time={time}
              value={draft.to}
              onChange={(to) => setDraft((d) => ({ ...d, to }))}
              error={errors.to}
            />
          </div>
          <div className="flex gap-2 sm:pt-6">
            <Button type="submit" variant="outline" className="max-lg:h-(--vs-control-touch)">
              Apply range
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setEditing(false);
                setErrors({});
              }}
              className="max-lg:h-(--vs-control-touch)"
            >
              Cancel
            </Button>
          </div>
        </form>
      ) : null}
    </div>
  );
}
