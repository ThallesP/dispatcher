const usd = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 2,
});

const num = new Intl.NumberFormat("en-US");

const usdWhole = new Intl.NumberFormat("en-US", {
  style: "currency",
  currency: "USD",
  maximumFractionDigits: 0,
});

/** Full comma-separated dollars, never abbreviated; cents dropped once they
 * stop mattering ($10,000+). */
export function fmtUsd(v: number): string {
  return Math.abs(v) >= 10_000 ? usdWhole.format(v) : usd.format(v);
}

/** Axis-tick currency: whole comma-separated dollars. */
export function fmtUsdTick(v: number): string {
  return usdWhole.format(v);
}

export function fmtNum(v: number): string {
  return num.format(v);
}

/** Format an integer cent amount as USD. */
export function fmtCents(cents: number): string {
  return usd.format(cents / 100);
}

export function fmtSignedPct(v: number): string {
  return `${v >= 0 ? "+" : ""}${v.toFixed(1)}%`;
}

/** Compact "7d" / "26h" / "45m" distance between two instants. */
export function fmtAgo(fromIso: string, toIso: string): string {
  const ms = Math.abs(new Date(toIso).getTime() - new Date(fromIso).getTime());
  const minutes = Math.round(ms / 60_000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}
