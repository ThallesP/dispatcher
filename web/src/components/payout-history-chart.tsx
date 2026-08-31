import { Bar, BarChart, CartesianGrid, LabelList, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "~/components/ui/chart";
import { fmtCents, fmtUsdTick } from "~/lib/format";
import type { PayoutMonth } from "~/queries/payouts";

const monthShort = new Intl.DateTimeFormat("en-US", {
  month: "short",
  timeZone: "UTC",
});
const monthLong = new Intl.DateTimeFormat("en-US", {
  month: "long",
  year: "numeric",
  timeZone: "UTC",
});

/** "2026-08" is a UTC calendar month, so parse it as UTC midnight — parsing it
 * locally would slide a westward timezone back into July. */
function monthDate(month: string): Date {
  return new Date(`${month}-01T00:00:00Z`);
}

interface Row {
  month: string;
  cash: number;
  credits: number;
  count: number;
  total: number;
}

/** Monthly payout columns, stacked by kind. Deliberately no cumulative line:
 * a running total needs its own y-scale, and the alignment between two scales
 * is arbitrary enough to invent a trend that isn't in the data. The lifetime
 * figure is a stat tile beside the chart instead. */
export function PayoutHistoryChart({ months }: { months: PayoutMonth[] }) {
  const rows: Row[] = months.map((m) => ({
    month: m.month,
    cash: m.cashCents,
    credits: m.creditsCents,
    count: m.count,
    total: m.cashCents + m.creditsCents,
  }));

  // Most workspaces only ever take cash. Rendering one series then means no
  // legend (its own title says what it is) and a plain unstacked column.
  const hasCredits = rows.some((r) => r.credits > 0);
  const chartConfig = {
    cash: { label: "Cash", color: "var(--chart-1)" },
    credits: { label: "Railway credits", color: "var(--chart-2)" },
  } satisfies ChartConfig;

  // Show the year on every tick once the window straddles one, so "Jan" is
  // never ambiguous.
  const years = new Set(rows.map((r) => r.month.slice(0, 4)));
  const tickFormat = (month: string) => {
    const label = monthShort.format(monthDate(month));
    return years.size > 1 ? `${label} ’${month.slice(2, 4)}` : label;
  };

  // Direct-label the biggest month, so the peak is readable without hovering.
  // Only in the single-series case: stacked, the label would have to hang off
  // the credits segment, which is missing entirely in a cash-only month.
  // Matched on value, not row index — a zero month renders no rectangle, so
  // LabelList's index runs ahead of the data after the first empty one.
  const maxTotal = Math.max(0, ...rows.map((r) => r.total));

  const yAxisWidth = Math.max(56, fmtUsdTick(maxTotal / 100).length * 7 + 14);

  return (
    <ChartContainer config={chartConfig} className="aspect-auto h-64 w-full">
      <BarChart accessibilityLayer data={rows} margin={{ top: 20, right: 12 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="month"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={16}
          tickFormatter={tickFormat}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={yAxisWidth}
          tickFormatter={(cents: number) => fmtUsdTick(cents / 100)}
        />
        <ChartTooltip
          cursor={{ fill: "var(--muted)" }}
          content={
            <ChartTooltipContent
              labelFormatter={(_, payload) =>
                monthLong.format(monthDate(payload?.[0]?.payload?.month ?? ""))
              }
              formatter={(value, name, item, index) => (
                <>
                  <div
                    className="h-2.5 w-1 shrink-0 rounded-[2px]"
                    style={{ background: item.color }}
                  />
                  <div className="flex flex-1 items-center justify-between gap-4 leading-none">
                    <span className="text-muted-foreground">
                      {chartConfig[name as keyof typeof chartConfig]?.label ?? name}
                    </span>
                    <span className="font-mono font-medium text-foreground tabular-nums">
                      {fmtCents(Number(value))}
                    </span>
                  </div>
                  {index === (hasCredits ? 1 : 0) && (
                    <div className="mt-0.5 flex basis-full items-center justify-between gap-4 border-t border-border/50 pt-1.5 leading-none">
                      <span className="text-muted-foreground">
                        {item.payload.count}{" "}
                        {item.payload.count === 1 ? "payout" : "payouts"}
                      </span>
                      <span className="font-mono font-medium text-foreground tabular-nums">
                        {fmtCents(item.payload.total)}
                      </span>
                    </div>
                  )}
                </>
              )}
            />
          }
        />
        <Bar
          dataKey="cash"
          stackId="payout"
          fill="var(--color-cash)"
          maxBarSize={24}
          // Square at the baseline, 4px rounded at the data end — which is the
          // top of the stack only when nothing sits above it.
          radius={hasCredits ? 0 : [4, 4, 0, 0]}
          // A 2px stroke in the surface color reads as the gap that separates
          // touching segments; the outer edge disappears against the card.
          stroke={hasCredits ? "var(--card)" : undefined}
          strokeWidth={hasCredits ? 2 : 0}
        >
          {!hasCredits && (
            <LabelList
              dataKey="total"
              content={(props) =>
                Number(props.value) === maxTotal && maxTotal > 0 ? (
                  <text
                    x={Number(props.x) + Number(props.width) / 2}
                    y={Number(props.y) - 6}
                    textAnchor="middle"
                    className="fill-muted-foreground text-[11px] tabular-nums"
                  >
                    {/* Whole dollars — cents on a chart annotation is noise. */}
                    {fmtUsdTick(maxTotal / 100)}
                  </text>
                ) : null
              }
            />
          )}
        </Bar>
        {hasCredits && (
          <Bar
            dataKey="credits"
            stackId="payout"
            fill="var(--color-credits)"
            maxBarSize={24}
            radius={[4, 4, 0, 0]}
            stroke="var(--card)"
            strokeWidth={2}
          />
        )}
        {hasCredits && <ChartLegend content={<ChartLegendContent />} />}
      </BarChart>
    </ChartContainer>
  );
}
