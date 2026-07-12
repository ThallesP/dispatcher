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
  SCHEDULE_PRESETS,
  scheduleDescription,
  type WithdrawAccounts,
  withdrawAccountsQuery,
  withdrawSettingsQuery,
} from "~/queries/withdraw";

/**
 * AutoWithdraw is a header button that opens the auto-withdraw setup dialog:
 * pick a payout account, choose how often it runs, and turn it on or off.
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
  const { enabled, withdrawalAccountId, schedule } = settings.data;

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
            currentSchedule={schedule}
            pending={save.isPending}
            error={save.error}
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
  currentSchedule,
  pending,
  error,
  onSave,
  onCancel,
}: {
  enabled: boolean;
  currentAccountId: string;
  currentSchedule: string;
  pending: boolean;
  error: Error | null;
  onSave: (body: {
    enabled: boolean;
    withdrawalAccountId?: string;
    schedule?: string;
  }) => void;
  onCancel: () => void;
}) {
  const accounts = useQuery(withdrawAccountsQuery);
  const [picked, setPicked] = useState(currentAccountId);
  // Default to the first usable account (banks sort first) until the user picks.
  const selected = picked || accounts.data?.accounts.find(accountReady)?.id || "";

  // A stored schedule that isn't one of the presets was hand-written, so open
  // straight into the cron editor with it.
  const [custom, setCustom] = useState(
    !SCHEDULE_PRESETS.some((p) => p.spec === currentSchedule),
  );
  const [spec, setSpec] = useState(currentSchedule);

  return (
    <>
      <DialogHeader>
        <DialogTitle>Auto-withdraw</DialogTitle>
        <DialogDescription>
          {accounts.data
            ? `Available now: ${fmtCents(accounts.data.availableBalance)} · runs ${custom ? "on the cron schedule below" : scheduleDescription(spec)} once your balance is over ${fmtCents(accounts.data.minimumBalance)}.`
            : "Automatically withdraw your Railway earnings to the account you choose."}
        </DialogDescription>
      </DialogHeader>

      <AccountList query={accounts} selected={selected} onSelect={setPicked} />

      <div className="grid gap-2">
        <span className="text-sm font-medium">Frequency</span>
        <div role="radiogroup" className="grid grid-cols-4 gap-1 rounded-lg border p-1">
          {SCHEDULE_PRESETS.map((p) => {
            const active = !custom && spec === p.spec;
            return (
              <button
                type="button"
                role="radio"
                aria-checked={active}
                key={p.spec}
                onClick={() => {
                  setCustom(false);
                  setSpec(p.spec);
                }}
                className={cn(
                  "cursor-pointer rounded-md px-2 py-1.5 text-sm",
                  active
                    ? "bg-muted font-medium"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {p.label}
              </button>
            );
          })}
          <button
            type="button"
            role="radio"
            aria-checked={custom}
            onClick={() => setCustom(true)}
            className={cn(
              "cursor-pointer rounded-md px-2 py-1.5 text-sm",
              custom
                ? "bg-muted font-medium"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            Custom
          </button>
        </div>
        {custom && (
          <>
            <input
              value={spec}
              onChange={(e) => setSpec(e.target.value)}
              placeholder="0 8 * * *"
              spellCheck={false}
              aria-label="Cron schedule"
              className="h-9 rounded-md border border-input bg-transparent px-3 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              Cron format in UTC: minute, hour, day of month, month, day of
              week — e.g. <code>0 8 * * 5</code> is Fridays at 08:00.
            </p>
          </>
        )}
      </div>

      {error && <p className="text-sm text-destructive">{error.message}</p>}

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
          onClick={() =>
            onSave({
              enabled: true,
              withdrawalAccountId: selected,
              schedule: spec.trim(),
            })
          }
          disabled={!selected || !spec.trim() || pending}
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
