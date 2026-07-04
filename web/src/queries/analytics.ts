import { queryOptions } from "@tanstack/react-query";
import { api } from "~/lib/api";

export interface PayoutPoint {
  sampledAt: string;
  totalPayout: number;
}

export interface MetricChange {
  current: number;
  previous: number | null;
  changePct: number | null;
}

export interface AnalyticsSummary {
  sampledAt: string;
  comparedTo: string | null;
  totalPayout: MetricChange;
  projects: MetricChange;
  recentProjects: MetricChange;
  activeProjects: MetricChange;
}

export interface TemplateAnalytics {
  templateId: string;
  name: string;
  code: string;
  status: string;
  health: number | null;
  projects: number;
  recentProjects: number;
  activeProjects: number;
  totalPayout: number;
  payoutPrevious: number | null;
  payoutChangePct: number | null;
}

export interface TemplateAnalyticsResponse {
  sampledAt: string;
  comparedTo: string | null;
  templates: TemplateAnalytics[];
}

export const payoutSeriesQuery = (days: number) =>
  queryOptions({
    queryKey: ["analytics", "payout", days],
    queryFn: ({ signal }) =>
      api
        .get("analytics/payout", { searchParams: { days }, signal })
        .json<PayoutPoint[]>(),
  });

export const summaryQuery = queryOptions({
  queryKey: ["analytics", "summary"],
  queryFn: ({ signal }) =>
    api.get("analytics/summary", { signal }).json<AnalyticsSummary | null>(),
});

export const templateAnalyticsQuery = queryOptions({
  queryKey: ["analytics", "templates"],
  queryFn: ({ signal }) =>
    api
      .get("analytics/templates", { signal })
      .json<TemplateAnalyticsResponse | null>(),
});
