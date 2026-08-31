import { useQuery } from "@tanstack/react-query";
import { CircleAlert, CircleCheck, Clock } from "lucide-react";
import { useMemo, useState } from "react";
import { PayoutHistoryChart } from "~/components/payout-history-chart";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Skeleton } from "~/components/ui/skeleton";
import { fmtCents, fmtNum } from "~/lib/format";
import { type Payout, payoutHistoryQuery } from "~/queries/payouts";

const dayFmt = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  year: "numeric",
});
const monthLong = new Intl.DateTimeFormat("en-US", {
  month: "long",
  year: "numeric",
  timeZone: "UTC",
});

/** Rows to reveal before the "show all" button — roughly a screenful, and
 * enough that a daily-withdrawal workspace still sees a full recent month. */
const collapsedRowCount = 15;

export function PayoutHistory() {
  const history = useQuery(payoutHistoryQuery);
  const [expanded, setExpanded] = useState(false);

  const groups = useMemo(
    () => groupByMonth(history.data?.payouts ?? []),
    [history.data],
  );
  // Cut on a month boundary rather than mid-group, so a visible month total
  // always matches the rows under it.
  const visible = expanded ? groups : takeRows(groups, collapsedRowCount);
  const hidden = (history.data?.payouts.length ?? 0) - countRows(visible);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Payouts</CardTitle>
        <CardDescription>
          Withdrawals from your Railway balance, by month
        </CardDescription>
      </CardHeader>

      {history.isPending && (
        <CardContent className="space-y-3">
          <Skeleton className="h-64 w-full" />
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </CardContent>
      )}

      {history.isError && (
        <CardContent>
          <p className="text-sm text-(--viz-critical)">
            Couldn&apos;t read payout history from Railway — check the server
            logs.
          </p>
        </CardContent>
      )}

      {history.data && history.data.payouts.length === 0 && (
        <CardContent>
          <p className="text-sm text-muted-foreground">
            No payouts yet. Railway pays out once your balance clears{" "}
            {fmtCents(10000)}.
          </p>
        </CardContent>
      )}

      {history.data && history.data.payouts.length > 0 && (
        <>
          <CardContent className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
            <Figure
              label="Total paid out"
              value={fmtCents(history.data.totals.lifetimeCents)}
            />
            <Figure
              label="Payouts"
              value={fmtNum(history.data.totals.count)}
              note={
                history.data.totals.firstPayoutAt
                  ? `since ${monthLong.format(new Date(history.data.totals.firstPayoutAt))}`
                  : undefined
              }
            />
            {history.data.totals.pendingCount > 0 && (
              <Figure
                label="In flight"
                value={fmtCents(history.data.totals.pendingCents)}
                note={`${history.data.totals.pendingCount} pending`}
              />
            )}
          </CardContent>

          <CardContent>
            <PayoutHistoryChart months={history.data.months} />
            {history.data.truncated && (
              <p className="pt-2 text-xs text-muted-foreground">
                Showing the most recent {fmtNum(history.data.payouts.length)}{" "}
                payouts — older months are not charted.
              </p>
            )}
          </CardContent>

          <CardContent className="overflow-x-auto">
            <table className="w-full text-sm">
              <caption className="sr-only">
                Every payout, newest first, grouped by month
              </caption>
              <thead>
                <tr className="border-b text-left text-xs text-muted-foreground">
                  <th className="pb-2 font-medium">Date</th>
                  <th className="pb-2 pl-3 font-medium">Destination</th>
                  <th className="pb-2 pl-3 font-medium">Status</th>
                  <th className="pb-2 pl-3 text-right font-medium">Amount</th>
                </tr>
              </thead>
              {visible.map((group) => (
                <tbody key={group.month}>
                  <tr className="border-b border-border/50 bg-muted/40">
                    <th
                      colSpan={3}
                      className="py-1.5 text-left text-xs font-medium text-muted-foreground"
                    >
                      {monthLong.format(new Date(`${group.month}-01T00:00:00Z`))}
                    </th>
                    <td className="py-1.5 pl-3 text-right text-xs font-medium tabular-nums text-muted-foreground">
                      {fmtCents(group.totalCents)}
                    </td>
                  </tr>
                  {group.payouts.map((p) => (
                    <PayoutRow key={p.id || p.createdAt} payout={p} />
                  ))}
                </tbody>
              ))}
            </table>
            {hidden > 0 && (
              <Button
                variant="ghost"
                size="sm"
                className="mt-3"
                onClick={() => setExpanded(true)}
              >
                Show {fmtNum(hidden)} older {hidden === 1 ? "payout" : "payouts"}
              </Button>
            )}
          </CardContent>
        </>
      )}
    </Card>
  );
}

/** Inline stat figure. Proportional (not tabular) digits — these sit alone,
 * not in a column, and equal-width digits read loose at this size. */
function Figure({
  label,
  value,
  note,
}: {
  label: string;
  value: string;
  note?: string;
}) {
  return (
    <div>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="text-xl font-semibold">{value}</div>
      {note && <div className="text-xs text-muted-foreground">{note}</div>}
    </div>
  );
}

function PayoutRow({ payout }: { payout: Payout }) {
  return (
    <tr className="border-b border-border/50 last:border-0">
      <td className="whitespace-nowrap py-2.5 pr-4 tabular-nums">
        {dayFmt.format(new Date(payout.createdAt))}
      </td>
      <td className="py-2.5 pl-3 text-muted-foreground">
        {payout.destination}
      </td>
      <td className="py-2.5 pl-3">
        <PayoutStatus status={payout.status} />
      </td>
      <td className="whitespace-nowrap py-2.5 pl-3 text-right font-medium tabular-nums">
        {fmtCents(payout.amountCents)}
      </td>
    </tr>
  );
}

/** Status always ships as icon + word, never color alone. Completed is the
 * norm and stays quiet; only the states that need attention take a color. */
function PayoutStatus({ status }: { status: string }) {
  const label = status.charAt(0) + status.slice(1).toLowerCase();
  if (status === "PENDING") {
    return (
      <span className="flex items-center gap-1 text-muted-foreground">
        <Clock className="size-3.5" />
        {label}
      </span>
    );
  }
  if (status === "COMPLETED") {
    return (
      <span className="flex items-center gap-1 text-(--viz-up)">
        <CircleCheck className="size-3.5" />
        {label}
      </span>
    );
  }
  return (
    <span className="flex items-center gap-1 text-(--viz-critical)">
      <CircleAlert className="size-3.5" />
      {label}
    </span>
  );
}

interface MonthGroup {
  month: string;
  totalCents: number;
  payouts: Payout[];
}

/** Group the newest-first payout list into the same months the chart plots, so
 * the table is the chart's readable twin. Failed payouts are listed but left
 * out of the month total, matching how the server builds the columns. */
function groupByMonth(payouts: Payout[]): MonthGroup[] {
  const groups: MonthGroup[] = [];
  for (const payout of payouts) {
    const month = payout.createdAt.slice(0, 7);
    let group = groups[groups.length - 1];
    if (!group || group.month !== month) {
      group = { month, totalCents: 0, payouts: [] };
      groups.push(group);
    }
    group.payouts.push(payout);
    if (payout.status === "COMPLETED" || payout.status === "PENDING") {
      group.totalCents += payout.amountCents;
    }
  }
  return groups;
}

function countRows(groups: MonthGroup[]): number {
  return groups.reduce((sum, g) => sum + g.payouts.length, 0);
}

/** Whole months until at least `min` rows are shown — always at least one. */
function takeRows(groups: MonthGroup[], min: number): MonthGroup[] {
  let rows = 0;
  const taken: MonthGroup[] = [];
  for (const group of groups) {
    taken.push(group);
    rows += group.payouts.length;
    if (rows >= min) break;
  }
  return taken;
}
