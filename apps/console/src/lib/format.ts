const integer = new Intl.NumberFormat("en", { maximumFractionDigits: 0 });

export function formatCount(n: number): string {
  return integer.format(n);
}

const dateFmt = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeZone: "UTC" });
const hourFmt = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "UTC",
});

// the control plane counts in utc hours and days; labels say so instead of
// silently shifting buckets into the viewer's zone
export function formatDate(iso: string): string {
  return dateFmt.format(new Date(iso));
}

export function formatHourUtc(iso: string): string {
  return `${hourFmt.format(new Date(iso))} UTC`;
}

export function formatBytes(n: number): string {
  return n < 1024 ? `${n} B` : `${(n / 1024).toFixed(1)} KiB`;
}

const dateTimeFmt = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
  hourCycle: "h23",
  timeZone: "UTC",
});

// audit rows and status changes need the time, not only the day
export function formatDateTimeUtc(iso: string): string {
  return `${dateTimeFmt.format(new Date(iso))} UTC`;
}
