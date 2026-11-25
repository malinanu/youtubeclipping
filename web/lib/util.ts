export function fmtTime(ms: number, withMs = false): string {
  if (!Number.isFinite(ms) || ms < 0) ms = 0;
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const tenths = Math.floor((ms % 1000) / 100);
  const base = h > 0 ? `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}` : `${m}:${String(s).padStart(2, "0")}`;
  return withMs ? `${base}.${tenths}` : base;
}

/** Parses "1:02:03.4", "2:03.5", "75.2" into milliseconds. Returns NaN when invalid. */
export function parseTime(s: string): number {
  const parts = s.trim().split(":").map((p) => p.trim());
  if (parts.length === 0 || parts.length > 3 || parts.some((p) => p === "" || isNaN(Number(p)))) return NaN;
  let sec = 0;
  for (const p of parts) sec = sec * 60 + Number(p);
  return Math.round(sec * 1000);
}

/** True when an error needs fresh YouTube cookies (bot check / private video). */
export function needsCookies(err: string): boolean {
  return /cookies|not a bot|Sign in to confirm|subscriber-only/i.test(err);
}

export function cn(...c: (string | false | null | undefined)[]) {
  return c.filter(Boolean).join(" ");
}

/** Index of the last word whose start <= t (binary search). */
export function wordIndexAt(words: { s: number }[], t: number): number {
  let lo = 0;
  let hi = words.length - 1;
  let ans = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (words[mid].s <= t) {
      ans = mid;
      lo = mid + 1;
    } else hi = mid - 1;
  }
  return ans;
}
