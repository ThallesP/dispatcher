import { useQuery } from "@tanstack/react-query";
import { ArrowDownRight, ArrowUpRight } from "lucide-react";
import { useState } from "react";
import { PayoutChart } from "~/components/payout-chart";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Skeleton } from "~/components/ui/skeleton";
import { fmtAgo, fmtNum, fmtSignedPct, fmtUsd } from "~/lib/format";
import {
  type MetricChange,
  payoutSeriesQuery,
  summaryQuery,
  type TemplateAnalytics,
  templateAnalyticsQuery,
  useRefreshAnalytics,
} from "~/queries/analytics";
import type { Route } from "./+types/analytics";

export function meta({}: Route.MetaArgs) {
  return [{ title: "Dispatcher" }];
}

const RANGES = [7, 30, 90] as const;

export default function Analytics() {
  const [days, setDays] = useState<number>(30);
  const summary = useQuery(summaryQuery);
  const series = useQuery(payoutSeriesQuery(days));
  const templates = useQuery(templateAnalyticsQuery);

  const hasData = summary.data != null;
  const comparedAgo =
    summary.data?.comparedTo != null
      ? fmtAgo(summary.data.comparedTo, summary.data.sampledAt)
      : null;

  return (
    <main className="viz mx-auto max-w-5xl space-y-6 p-6">
      <header>
        <h1 className="font-heading text-xl font-semibold">Analytics</h1>
        <p className="text-sm text-muted-foreground">
          {summary.data
            ? `Last sampled ${fmtAgo(summary.data.sampledAt, new Date().toISOString())} ago`
            : "Template snapshots from your workspace"}
        </p>
      </header>

      {summary.isPending && <DashboardSkeleton />}
      {summary.isError && (
        <p className="text-sm text-(--viz-critical)">
          Failed to load analytics — is the API up?
        </p>
      )}
      {summary.isSuccess && !hasData && <EmptyState />}

      {summary.data && (
        <section className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <StatTile
            label="Total payout"
            value={fmtUsd(summary.data.totalPayout.current)}
            change={summary.data.totalPayout}
            ago={comparedAgo}
          />
          <StatTile
            label="Active projects"
            value={fmtNum(summary.data.activeProjects.current)}
            change={summary.data.activeProjects}
            ago={comparedAgo}
          />
          <StatTile
            label="Recent projects"
            value={fmtNum(summary.data.recentProjects.current)}
            change={summary.data.recentProjects}
            ago={comparedAgo}
          />
          <StatTile
            label="Total projects"
            value={fmtNum(summary.data.projects.current)}
            change={summary.data.projects}
            ago={comparedAgo}
          />
        </section>
      )}

      {hasData && (
        <Card>
          <CardHeader>
            <CardTitle>Total payout</CardTitle>
            <CardDescription>
              Cumulative kickback, stacked by template
            </CardDescription>
            <CardAction className="flex gap-1">
              {RANGES.map((r) => (
                <Button
                  key={r}
                  size="xs"
                  variant={r === days ? "secondary" : "ghost"}
                  onClick={() => setDays(r)}
                >
                  {r}d
                </Button>
              ))}
            </CardAction>
          </CardHeader>
          <CardContent>
            {series.isPending ? (
              <Skeleton className="h-64 w-full" />
            ) : series.data && series.data.points.length > 0 ? (
              <PayoutChart data={series.data} />
            ) : (
              <p className="py-8 text-center text-sm text-muted-foreground">
                No samples in this range yet.
              </p>
            )}
          </CardContent>
        </Card>
      )}

      {hasData && templates.isPending && <TableCardSkeleton />}
      {templates.data && templates.data.templates.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Templates</CardTitle>
            <CardDescription>
              {comparedAgo
                ? `Latest snapshot · payout change vs ${comparedAgo} ago`
                : "Latest snapshot"}
            </CardDescription>
          </CardHeader>
          <CardContent className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-xs text-muted-foreground">
                  <th className="pb-2 font-medium">Template</th>
                  <th className="pb-2 pl-3 text-right font-medium">Projects</th>
                  <th className="pb-2 pl-3 text-right font-medium">Active</th>
                  <th className="pb-2 pl-3 text-right font-medium">Payout</th>
                  <th className="pb-2 pl-3 text-right font-medium">Δ payout</th>
                </tr>
              </thead>
              <tbody>
                {templates.data.templates.map((t) => (
                  <TemplateRow key={t.templateId} template={t} />
                ))}
              </tbody>
            </table>
          </CardContent>
        </Card>
      )}
    </main>
  );
}

// Skeletons mirror the real layout so nothing jumps when data lands.
// EmptyState shows before the first snapshot exists (fresh sign-in) and lets
// the user collect one now instead of waiting for the hourly cron.
function EmptyState() {
  const refresh = useRefreshAnalytics();
  return (
    <Card>
      <CardContent className="flex flex-col items-start gap-3">
        <p className="text-sm text-muted-foreground">
          No snapshots yet. The collector runs hourly, or you can grab the
          first one right now.
        </p>
        <Button onClick={() => refresh.mutate()} disabled={refresh.isPending}>
          {refresh.isPending ? "Refreshing…" : "Refresh now"}
        </Button>
        {refresh.isError && (
          <p className="text-sm text-(--viz-critical)">
            Couldn&apos;t refresh — check the server logs and try again.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function DashboardSkeleton() {
  return (
    <>
      <section className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <Card key={i} size="sm">
            <CardContent className="space-y-2">
              <Skeleton className="h-3 w-20" />
              <Skeleton className="h-7 w-24" />
              <Skeleton className="h-3 w-28" />
            </CardContent>
          </Card>
        ))}
      </section>
      <Card>
        <CardHeader>
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-3 w-56" />
        </CardHeader>
        <CardContent>
          <Skeleton className="h-64 w-full" />
        </CardContent>
      </Card>
      <TableCardSkeleton />
    </>
  );
}

function TableCardSkeleton() {
  return (
    <Card>
      <CardHeader>
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-3 w-64" />
      </CardHeader>
      <CardContent className="space-y-3">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-8 w-full" />
        ))}
      </CardContent>
    </Card>
  );
}

function StatTile({
  label,
  value,
  change,
  ago,
}: {
  label: string;
  value: string;
  change: MetricChange;
  ago: string | null;
}) {
  return (
    <Card size="sm">
      <CardContent className="space-y-1">
        <div className="text-xs text-muted-foreground">{label}</div>
        {/* Full (unabbreviated) values can get long — step the size down so
            something like $1,234,567,890 still fits the quarter-width tile. */}
        <div
          className={`font-semibold tabular-nums ${
            value.length > 13
              ? "text-base"
              : value.length > 10
                ? "text-xl"
                : "text-2xl"
          }`}
        >
          {value}
        </div>
        {change.changePct != null && ago != null ? (
          <div className="flex items-center gap-1 text-xs">
            <span
              className={`flex items-center gap-0.5 font-medium ${
                change.changePct >= 0
                  ? "text-(--viz-up)"
                  : "text-(--viz-down)"
              }`}
            >
              {change.changePct >= 0 ? (
                <ArrowUpRight className="size-3.5" />
              ) : (
                <ArrowDownRight className="size-3.5" />
              )}
              {fmtSignedPct(change.changePct)}
            </span>
            <span className="text-muted-foreground">vs {ago} ago</span>
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">
            no comparison yet
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function TemplateRow({ template: t }: { template: TemplateAnalytics }) {
  return (
    <tr className="border-b border-border/50 last:border-0">
      <td className="py-2.5 pr-4">
        <div className="font-medium">{t.name}</div>
        <div className="text-xs text-muted-foreground">{t.status.toLowerCase()}</div>
      </td>
      <td className="py-2.5 pl-3 text-right tabular-nums">
        {fmtNum(t.projects)}
      </td>
      <td className="py-2.5 pl-3 text-right tabular-nums">
        {fmtNum(t.activeProjects)}
      </td>
      <td className="whitespace-nowrap py-2.5 pl-3 text-right font-medium tabular-nums">
        {fmtUsd(t.totalPayout)}
      </td>
      <td
        className={`py-2.5 pl-3 text-right tabular-nums ${
          t.payoutChangePct == null
            ? "text-muted-foreground"
            : t.payoutChangePct >= 0
              ? "text-(--viz-up)"
              : "text-(--viz-down)"
        }`}
      >
        {t.payoutChangePct != null ? fmtSignedPct(t.payoutChangePct) : "—"}
      </td>
    </tr>
  );
}
