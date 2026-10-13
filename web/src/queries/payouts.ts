import { queryOptions } from "@tanstack/react-query";
import { api } from "~/lib/api";

/** One withdrawal, mirrored from Railway into Dispatcher's database by the
 * collector. Credit payouts have no Railway id and are always reported as
 * COMPLETED — they settle the moment they exist. */
export interface Payout {
  id: string;
  createdAt: string;
  amountCents: number;
  status: string;
  kind: "cash" | "credits";
  /** "Bank ••8149" / "Card ••4242" / "Railway credits". */
  destination: string;
}

/** One day of the chart. Amounts are cumulative from the start of the selected
 * window, and count is the running number of payouts behind them. */
export interface PayoutPoint {
  date: string; // YYYY-MM-DD
  cashCents: number;
  creditsCents: number;
  count: number;
}

/** The selected range against the range of equal length before it. */
export interface PayoutWindow {
  days: number;
  totalCents: number;
  previousCents: number;
  changePct: number | null;
  count: number;
}

export interface PayoutTotals {
  lifetimeCents: number;
  cashCents: number;
  creditsCents: number;
  pendingCents: number;
  pendingCount: number;
  count: number;
  firstPayoutAt: string | null;
  lastPayoutAt: string | null;
}

export interface PayoutHistory {
  points: PayoutPoint[];
  window: PayoutWindow;
  totals: PayoutTotals;
  /** Every row inside the selected window; totalRows is how many exist in all. */
  payouts: Payout[];
  totalRows: number;
}

/** Served from Dispatcher's own database — the collector mirrors Railway's
 * paginated history on every refresh and after each withdrawal, so the
 * dashboard never waits on Railway to paint. */
export const payoutHistoryQuery = (days: number) =>
  queryOptions({
    queryKey: ["payouts", "history", days],
    queryFn: ({ signal }) =>
      api
        .get("payouts", { searchParams: { days }, signal })
        .json<PayoutHistory>(),
  });
