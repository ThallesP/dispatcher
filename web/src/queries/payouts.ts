import { queryOptions } from "@tanstack/react-query";
import { api } from "~/lib/api";

/** One withdrawal from Railway. Credits payouts have no id and are always
 * reported as COMPLETED — they settle the moment they exist. */
export interface Payout {
  id: string;
  createdAt: string;
  amountCents: number;
  status: string;
  kind: "cash" | "credits";
  /** "Bank ••8149" / "Card ••4242" / "Railway credits". */
  destination: string;
}

/** One column of the payout chart: a UTC calendar month, split by kind so the
 * two stack. Quiet months are zero-filled by the server. */
export interface PayoutMonth {
  month: string; // YYYY-MM
  cashCents: number;
  creditsCents: number;
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
  payouts: Payout[];
  months: PayoutMonth[];
  totals: PayoutTotals;
  /** The history is longer than the server's page walk read, so the oldest
   * months are missing from the chart. */
  truncated: boolean;
}

/** Payout history is read live from Railway rather than snapshotted: it's
 * Railway's own immutable record, so there's nothing to drift. */
export const payoutHistoryQuery = queryOptions({
  queryKey: ["payouts", "history"],
  queryFn: ({ signal }) => api.get("payouts", { signal }).json<PayoutHistory>(),
});
