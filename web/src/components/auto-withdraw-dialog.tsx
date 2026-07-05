import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useState } from "react";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { Skeleton } from "~/components/ui/skeleton";
import { fmtCents } from "~/lib/format";
import { cn } from "~/lib/utils";
import {
  accountLabel,
  accountReady,
  saveWithdrawSettings,
  type WithdrawAccounts,
  withdrawAccountsQuery,
  withdrawSettingsQuery,
} from "~/queries/withdraw";

/**
 * AutoWithdraw is a header button that opens the auto-withdraw setup dialog:
 * pick a payout account and turn the daily withdrawal on or off.
 */
export function AutoWithdraw() {
  const qc = useQueryClient();
  const settings = useQuery(withdrawSettingsQuery);
  const [open, setOpen] = useState(false);

  const save = useMutation({
    mutationFn: saveWithdrawSettings,
    onSuccess: (data) => {
      qc.setQueryData(withdrawSettingsQuery.queryKey, data);
      setOpen(false);
    },
  });

  if (!settings.data) return null;
  const { enabled, withdrawalAccountId } = settings.data;

  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        {enabled ? "Auto-withdraw · On" : "Auto-withdraw"}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <SetupView
            enabled={enabled}
            currentAccountId={withdrawalAccountId}
            pending={save.isPending}
            failed={save.isError}
            onSave={save.mutate}
            onCancel={() => setOpen(false)}
          />
        </DialogContent>
      </Dialog>
    </>
  );
}

function SetupView({
  enabled,
  currentAccountId,
  pending,
  failed,
  onSave,
  onCancel,
}: {
  enabled: boolean;
  currentAccountId: string;
  pending: boolean;
  failed: boolean;
  onSave: (body: { enabled: boolean; withdrawalAccountId?: string }) => void;
  onCancel: () => void;
}) {
  const accounts = useQuery(withdrawAccountsQuery);
  const [picked, setPicked] = useState(currentAccountId);
  // Default to the first usable account (banks sort first) until the user picks.
  const selected = picked || accounts.data?.accounts.find(accountReady)?.id || "";

  return (
    <>
      <DialogHeader>
        <DialogTitle>Auto-withdraw</DialogTitle>
        <DialogDescription>
          {accounts.data
            ? `Available now: ${fmtCents(accounts.data.availableBalance)} · runs daily at 08:00 UTC once your balance is over ${fmtCents(accounts.data.minimumBalance)}.`
            : "Automatically withdraw your Railway earnings to the account you choose, once a day."}
        </DialogDescription>
      </DialogHeader>

      <AccountList query={accounts} selected={selected} onSelect={setPicked} />

      {failed && (
        <p className="text-sm text-destructive">
          Couldn&apos;t save — please try again.
        </p>
      )}

      <DialogFooter>
        {enabled ? (
          <Button
            variant="ghost"
            onClick={() => onSave({ enabled: false })}
            disabled={pending}
          >
            Turn off
          </Button>
        ) : (
          <Button variant="ghost" onClick={onCancel} disabled={pending}>
            Cancel
          </Button>
        )}
        <Button
          onClick={() => onSave({ enabled: true, withdrawalAccountId: selected })}
          disabled={!selected || pending}
        >
          {pending && <Loader2 className="size-4 animate-spin" />}
          {enabled ? "Save" : "Enable auto-withdraw"}
        </Button>
      </DialogFooter>
    </>
  );
}

function AccountList({
  query,
  selected,
  onSelect,
}: {
  query: ReturnType<typeof useQuery<WithdrawAccounts>>;
  selected: string;
  onSelect: (id: string) => void;
}) {
  if (query.isPending) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-14 w-full rounded-lg" />
        <Skeleton className="h-14 w-full rounded-lg" />
      </div>
    );
  }
  if (query.isError) {
    return (
      <p className="text-sm text-destructive">
        Couldn&apos;t load your withdrawal accounts.
      </p>
    );
  }
  if (!query.data || query.data.accounts.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No withdrawal accounts yet. Add one in your{" "}
        <a
          href="https://railway.com/workspace/earn"
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-2 hover:text-foreground"
        >
          Railway earnings settings
        </a>
        , then come back.
      </p>
    );
  }

  return (
    <div role="radiogroup" className="grid gap-2">
      {query.data.accounts.map((a) => {
        const usable = accountReady(a);
        const checked = selected === a.id;
        return (
          <button
            type="button"
            role="radio"
            aria-checked={checked}
            key={a.id}
            disabled={!usable}
            onClick={() => onSelect(a.id)}
            className={cn(
              "flex items-center gap-3 rounded-lg border p-3 text-left",
              usable ? "cursor-pointer hover:bg-muted/50" : "opacity-60",
              checked && "border-primary",
            )}
          >
            <span
              className={cn(
                "size-4 shrink-0 rounded-full border",
                checked ? "border-[5px] border-primary" : "border-input",
              )}
            />
            <span className="flex-1">
              <span className="block text-sm font-medium">{accountLabel(a)}</span>
              {!usable && (
                <span className="block text-xs text-muted-foreground">
                  Finish setup in Railway to use this account
                </span>
              )}
            </span>
          </button>
        );
      })}
    </div>
  );
}
