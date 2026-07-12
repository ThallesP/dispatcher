import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "~/components/ui/chart";
import { fmtSignedPct, fmtUsd, fmtUsdTick } from "~/lib/format";
import type { PayoutSeriesResponse } from "~/queries/analytics";

const dayFmt = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
});
const timeFmt = new Intl.DateTimeFormat("en-US", {
  hour: "numeric",
  minute: "2-digit",
});
const fullFmt = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "numeric",
  minute: "2-digit",
});

// Series color = slot order (--chart-1..5, validated categorical order);
// the "Other" fold always wears the neutral gray, never a series hue.
function seriesColor(key: string, index: number): string {
  return key === "other" ? "var(--chart-other)" : `var(--chart-${index + 1})`;
}

/** Signed growth from the window's first sample to the hovered one, e.g.
 * "+12.4%"; "—" when there is no baseline to compare against. */
function growthSince(base: unknown, current: unknown): string {
  const b = Number(base);
  const c = Number(current);
  return b > 0 && Number.isFinite(c) ? fmtSignedPct(((c - b) / b) * 100) : "—";
}

export function PayoutChart({ data }: { data: PayoutSeriesResponse }) {
  const { series, points } = data;

  const chartConfig = Object.fromEntries(
    series.map((s, i) => [s.key, { label: s.name, color: seriesColor(s.key, i) }]),
  ) satisfies ChartConfig;

  const rows: Record<string, number | string>[] = points.map((p) => ({
    sampledAt: p.sampledAt,
    ...p.values,
  }));
  const stackTotal = (row: Record<string, unknown>) =>
    series.reduce((sum, s) => sum + (Number(row[s.key]) || 0), 0);

  // Size the axis gutter to the largest tick label so full (non-abbreviated)
  // dollar amounts never clip, however big the payout grows. ~7px per char
  // at the 12px tick size, plus the 8px tick margin.
  const maxTotal = Math.max(0, ...rows.map(stackTotal));
  const yAxisWidth = Math.max(56, fmtUsdTick(maxTotal * 1.15).length * 7 + 14);

  const first = points[0];
  const last = points[points.length - 1];
  const spansDays =
    points.length > 1 &&
    new Date(last.sampledAt).getTime() - new Date(first.sampledAt).getTime() >
      3 * 24 * 60 * 60 * 1000;

  // Tooltip deltas compare the hovered sample against the first one in the
  // visible range, so the baseline follows the 7d/30d/90d picker.
  const baseline = rows[0];
  const baselineLabel = (spansDays ? dayFmt : fullFmt).format(
    new Date(first.sampledAt),
  );

  return (
    <ChartContainer config={chartConfig} className="aspect-auto h-64 w-full">
      <AreaChart data={rows} margin={{ top: 8, right: 12 }}>
        <CartesianGrid vertical={false} />
        <XAxis
          dataKey="sampledAt"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={48}
          tickFormatter={(v: string) =>
            (spansDays ? dayFmt : timeFmt).format(new Date(v))
          }
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          width={yAxisWidth}
          domain={[0, "auto"]}
          tickFormatter={(v: number) => fmtUsdTick(v)}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(label, payload) =>
                fullFmt.format(
                  new Date(payload?.[0]?.payload?.sampledAt ?? String(label)),
                )
              }
              formatter={(value, name, item, index) => (
                <>
                  <div
                    className="h-2.5 w-1 shrink-0 rounded-[2px]"
                    style={{ background: item.color }}
                  />
                  <div className="flex flex-1 items-center justify-between gap-4 leading-none">
                    <span className="text-muted-foreground">
                      {chartConfig[name as string]?.label ?? name}
                    </span>
                    <span className="flex items-baseline gap-2">
                      <span className="font-mono font-medium text-foreground tabular-nums">
                        {fmtUsd(Number(value))}
                      </span>
                      <span className="min-w-12 text-right font-mono text-muted-foreground tabular-nums">
                        {growthSince(baseline?.[name as string], value)}
                      </span>
                    </span>
                  </div>
                  {index === series.length - 1 && (
                    <>
                      {series.length > 1 && (
                        <div className="mt-0.5 flex basis-full items-center justify-between gap-4 border-t border-border/50 pt-1.5 leading-none">
                          <span className="text-muted-foreground">Total</span>
                          <span className="flex items-baseline gap-2">
                            <span className="font-mono font-medium text-foreground tabular-nums">
                              {fmtUsd(stackTotal(item.payload))}
                            </span>
                            <span className="min-w-12 text-right font-mono text-muted-foreground tabular-nums">
                              {growthSince(
                                stackTotal(baseline),
                                stackTotal(item.payload),
                              )}
                            </span>
                          </span>
                        </div>
                      )}
                      <div className="basis-full text-right text-[10px] leading-none text-muted-foreground">
                        Δ since {baselineLabel}
                      </div>
                    </>
                  )}
                </>
              )}
            />
          }
        />
        {series.map((s, i) => (
          <Area
            key={s.key}
            dataKey={s.key}
            stackId="payout"
            type="monotone"
            stroke={seriesColor(s.key, i)}
            strokeWidth={2}
            fill={seriesColor(s.key, i)}
            fillOpacity={0.1}
            dot={false}
            activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }}
          />
        ))}
        {series.length > 1 && <ChartLegend content={<ChartLegendContent />} />}
      </AreaChart>
    </ChartContainer>
  );
}
