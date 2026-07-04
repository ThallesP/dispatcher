import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "~/components/ui/chart";
import { fmtUsd, fmtUsdTick } from "~/lib/format";
import type { PayoutPoint } from "~/queries/analytics";

// Railway purple (pink-500 / pink-600 from railway.com/design/color), stepped
// per surface. Validated: >= 3:1 contrast on both surfaces.
const chartConfig = {
  totalPayout: {
    label: "Total payout",
    theme: { light: "#853bce", dark: "#a667e4" },
  },
} satisfies ChartConfig;

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

export function PayoutChart({ points }: { points: PayoutPoint[] }) {
  const first = points[0];
  const last = points[points.length - 1];
  const spansDays =
    points.length > 1 &&
    new Date(last.sampledAt).getTime() - new Date(first.sampledAt).getTime() >
      3 * 24 * 60 * 60 * 1000;

  return (
    <ChartContainer config={chartConfig} className="aspect-auto h-64 w-full">
      <AreaChart data={points} margin={{ top: 8, right: 12 }}>
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
          width={64}
          domain={["auto", "auto"]}
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
              formatter={(value) => (
                <>
                  <div
                    className="h-2.5 w-1 shrink-0 rounded-[2px]"
                    style={{ background: "var(--color-totalPayout)" }}
                  />
                  <div className="flex flex-1 items-center justify-between gap-4 leading-none">
                    <span className="text-muted-foreground">Total payout</span>
                    <span className="font-mono font-medium text-foreground tabular-nums">
                      {fmtUsd(Number(value))}
                    </span>
                  </div>
                </>
              )}
            />
          }
        />
        <Area
          dataKey="totalPayout"
          type="monotone"
          stroke="var(--color-totalPayout)"
          strokeWidth={2}
          fill="var(--color-totalPayout)"
          fillOpacity={0.1}
          dot={false}
          activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }}
        />
      </AreaChart>
    </ChartContainer>
  );
}
