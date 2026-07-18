import { queryOptions } from "@tanstack/react-query";
import { api } from "~/lib/api";

export type NotificationKind = "discord" | "slack" | "ntfy" | "custom";
export type NotificationEventType =
  | "payout"
  | "health_drop"
  | "weekly_summary";

export interface NotificationTargetInput {
  name: string;
  kind: NotificationKind;
  url: string;
  headers: Record<string, string>;
  bodyTemplate: string;
  enabled: boolean;
  onPayout: boolean;
  onHealthDrop: boolean;
  onWeeklySummary: boolean;
}

export interface NotificationTarget extends NotificationTargetInput {
  id: number;
  createdAt: string;
  updatedAt: string;
}

export interface NotificationPreset {
  headers: Record<string, string>;
  bodyTemplate: string;
}

export type NotificationPresets = Record<
  NotificationKind,
  NotificationPreset
>;

export interface NotificationTestResult {
  ok: boolean;
  statusCode: number;
  error?: string;
}

export const notificationTargetsQuery = queryOptions({
  queryKey: ["notify", "targets"],
  queryFn: ({ signal }) =>
    api.get("notify/targets", { signal }).json<NotificationTarget[]>(),
});

export const notificationPresetsQuery = queryOptions({
  queryKey: ["notify", "presets"],
  queryFn: ({ signal }) =>
    api.get("notify/presets", { signal }).json<NotificationPresets>(),
  staleTime: Infinity,
});

async function responseError(
  res: Response,
  fallback: string,
): Promise<Error> {
  const detail = (await res.json().catch(() => null)) as {
    error?: string;
  } | null;
  return new Error(detail?.error ?? fallback);
}

export async function saveNotificationTarget(
  target: NotificationTargetInput & { id?: number },
): Promise<NotificationTarget> {
  const { id, ...body } = target;
  const res = id
    ? await api.put(`notify/targets/${id}`, {
        json: body,
        throwHttpErrors: false,
      })
    : await api.post("notify/targets", {
        json: body,
        throwHttpErrors: false,
      });
  if (!res.ok) {
    throw await responseError(res, "Couldn’t save the notification target.");
  }
  return res.json<NotificationTarget>();
}

export async function deleteNotificationTarget(id: number): Promise<void> {
  const res = await api.delete(`notify/targets/${id}`, {
    throwHttpErrors: false,
  });
  if (!res.ok) {
    throw await responseError(res, "Couldn’t delete the notification target.");
  }
}

export async function testNotificationDraft(
  target: NotificationTargetInput,
  event: NotificationEventType,
): Promise<NotificationTestResult> {
  const res = await api.post("notify/test", {
    json: { ...target, event },
    throwHttpErrors: false,
  });
  const result = (await res.json().catch(() => null)) as
    | NotificationTestResult
    | { error?: string }
    | null;
  if (!res.ok) {
    throw new Error(
      result && "error" in result && result.error
        ? result.error
        : "The test notification failed.",
    );
  }
  return result as NotificationTestResult;
}

export async function testSavedNotificationTarget(
  id: number,
  event: NotificationEventType,
): Promise<NotificationTestResult> {
  const res = await api.post(`notify/targets/${id}/test`, {
    json: { event },
    throwHttpErrors: false,
  });
  const result = (await res.json().catch(() => null)) as
    | NotificationTestResult
    | { error?: string }
    | null;
  if (!res.ok) {
    throw new Error(
      result && "error" in result && result.error
        ? result.error
        : "The test notification failed.",
    );
  }
  return result as NotificationTestResult;
}

