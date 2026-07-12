import { queryOptions } from "@tanstack/react-query";
import { api } from "~/lib/api";

export interface WithdrawSettings {
  enabled: boolean;
  withdrawalAccountId: string;
  /** Standard cron spec, evaluated in UTC. */
  schedule: string;
}

/**
 * Friendly cadences offered in the setup dialog; anything else is entered as
 * a raw cron spec. All presets fire at 08:00 UTC to catch the US-morning ACH
 * window (mirrors the backend default).
 */
export const SCHEDULE_PRESETS = [
  { spec: "0 8 * * *", label: "Daily", description: "every day at 08:00 UTC" },
  {
    spec: "0 8 * * 1",
    label: "Weekly",
    description: "Mondays at 08:00 UTC",
  },
  {
    spec: "0 8 1 * *",
    label: "Monthly",
    description: "on the 1st at 08:00 UTC",
  },
] as const;

/** Human phrasing of a schedule, falling back to the raw spec for custom crons. */
export function scheduleDescription(spec: string): string {
  const preset = SCHEDULE_PRESETS.find((p) => p.spec === spec);
  return preset ? preset.description : `on cron schedule “${spec}” (UTC)`;
}

export interface WithdrawalAccount {
  id: string;
  platform: string;
  stripeConnectInfo: {
    hasOnboarded: boolean;
    needsAttention: boolean;
    bankLast4: string;
    cardLast4: string;
  };
}

export interface WithdrawAccounts {
  availableBalance: number; // cents
  minimumBalance: number; // cents
  accounts: WithdrawalAccount[];
}

export const withdrawSettingsQuery = queryOptions({
  queryKey: ["withdraw", "settings"],
  queryFn: ({ signal }) =>
    api.get("withdraw/settings", { signal }).json<WithdrawSettings>(),
});

export const withdrawAccountsQuery = queryOptions({
  queryKey: ["withdraw", "accounts"],
  queryFn: ({ signal }) =>
    api.get("withdraw/accounts", { signal }).json<WithdrawAccounts>(),
  // Bank accounts list before cards.
  select: (d) => ({
    ...d,
    accounts: [...d.accounts].sort(
      (a, b) =>
        Number(!a.stripeConnectInfo.bankLast4) -
        Number(!b.stripeConnectInfo.bankLast4),
    ),
  }),
});

export async function saveWithdrawSettings(body: {
  enabled: boolean;
  withdrawalAccountId?: string;
  schedule?: string;
}): Promise<WithdrawSettings> {
  // Surface the server's message (e.g. why a cron spec was rejected) instead
  // of ky's generic HTTPError text.
  const res = await api.post("withdraw/settings", {
    json: body,
    throwHttpErrors: false,
  });
  if (!res.ok) {
    const detail = (await res.json().catch(() => null)) as {
      error?: string;
    } | null;
    throw new Error(detail?.error ?? "Couldn’t save — please try again.");
  }
  return res.json<WithdrawSettings>();
}

/** Human label for a Stripe Connect payout destination. */
export function accountLabel(a: WithdrawalAccount): string {
  const { bankLast4, cardLast4 } = a.stripeConnectInfo;
  if (bankLast4) return `Bank •••• ${bankLast4}`;
  if (cardLast4) return `Card •••• ${cardLast4}`;
  return a.platform || "Withdrawal account";
}

/** An account can only receive payouts once Stripe onboarding is complete. */
export function accountReady(a: WithdrawalAccount): boolean {
  return a.stripeConnectInfo.hasOnboarded && !a.stripeConnectInfo.needsAttention;
}
