import { queryOptions } from "@tanstack/react-query";
import { api } from "~/lib/api";

export interface WithdrawSettings {
  enabled: boolean;
  withdrawalAccountId: string;
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

export function saveWithdrawSettings(body: {
  enabled: boolean;
  withdrawalAccountId?: string;
}): Promise<WithdrawSettings> {
  return api.post("withdraw/settings", { json: body }).json<WithdrawSettings>();
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
