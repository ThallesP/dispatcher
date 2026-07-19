import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowUpRight,
  Bell,
  Check,
  ChevronDown,
  ChevronLeft,
  Copy,
  Loader2,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";
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
import { templateHelpPrompt } from "~/lib/notify-prompt";
import { cn } from "~/lib/utils";
import {
  deleteNotificationTarget,
  notificationPresetsQuery,
  notificationTargetsQuery,
  saveNotificationTarget,
  testNotificationDraft,
  type NotificationEventType,
  type NotificationKind,
  type NotificationPreset,
  type NotificationPresets,
  type NotificationTarget,
  type NotificationTargetInput,
} from "~/queries/notify";

const KINDS: {
  value: NotificationKind;
  label: string;
  urlPlaceholder: string;
  urlHint: string;
}[] = [
  {
    value: "discord",
    label: "Discord",
    urlPlaceholder: "https://discord.com/api/webhooks/…",
    urlHint: "Server settings → Integrations → Webhooks → Copy webhook URL.",
  },
  {
    value: "slack",
    label: "Slack",
    urlPlaceholder: "https://hooks.slack.com/services/…",
    urlHint: "Paste an incoming-webhook URL for your Slack workspace.",
  },
  {
    value: "ntfy",
    label: "ntfy",
    urlPlaceholder: "https://ntfy.sh/your-topic",
    urlHint: "Your topic URL — self-hosted ntfy servers work too.",
  },
  {
    value: "custom",
    label: "Custom",
    urlPlaceholder: "https://example.com/webhook",
    urlHint: "Dispatcher POSTs the rendered body template to this URL.",
  },
];

const EVENTS: {
  value: NotificationEventType;
  label: string;
  field: "onPayout" | "onHealthDrop" | "onWeeklySummary";
}[] = [
  { value: "payout", label: "Payouts", field: "onPayout" },
  { value: "health_drop", label: "Health drops", field: "onHealthDrop" },
  {
    value: "weekly_summary",
    label: "Weekly summary",
    field: "onWeeklySummary",
  },
];

const inputClass =
  "h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring";
const textareaClass =
  "w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring";

function targetInput(target: NotificationTarget): NotificationTargetInput {
  return {
    name: target.name,
    kind: target.kind,
    url: target.url,
    headers: target.headers,
    bodyTemplate: target.bodyTemplate,
    enabled: target.enabled,
    onPayout: target.onPayout,
    onHealthDrop: target.onHealthDrop,
    onWeeklySummary: target.onWeeklySummary,
  };
}

function newTarget(preset: NotificationPreset): NotificationTargetInput {
  return {
    name: "",
    kind: "discord",
    url: "",
    headers: { ...preset.headers },
    bodyTemplate: preset.bodyTemplate,
    enabled: false,
    onPayout: true,
    onHealthDrop: true,
    onWeeklySummary: true,
  };
}

export function NotificationsDialog() {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<NotificationTarget | "new" | null>(
    null,
  );
  const targets = useQuery(notificationTargetsQuery);
  const presets = useQuery(notificationPresetsQuery);

  const handleOpen = (next: boolean) => {
    setOpen(next);
    if (!next) setEditing(null);
  };

  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        <Bell />
        Notifications
      </Button>
      <Dialog open={open} onOpenChange={handleOpen}>
        <DialogContent className="max-h-[calc(100vh-2rem)] overflow-y-auto sm:max-w-xl">
          {editing && presets.data ? (
            <NotificationEditor
              key={editing === "new" ? "new" : editing.id}
              initial={
                editing === "new"
                  ? newTarget(presets.data.discord)
                  : targetInput(editing)
              }
              id={editing === "new" ? undefined : editing.id}
              presets={presets.data}
              onBack={() => setEditing(null)}
              onSaved={() => setEditing(null)}
            />
          ) : (
            <NotificationTargetList
              targets={targets}
              presetsReady={Boolean(presets.data)}
              onAdd={() => setEditing("new")}
              onEdit={setEditing}
            />
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function Switch({
  checked,
  disabled,
  label,
  onToggle,
}: {
  checked: boolean;
  disabled?: boolean;
  label: string;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={onToggle}
      className={cn(
        "relative h-5 w-9 shrink-0 cursor-pointer rounded-full transition-colors disabled:opacity-50",
        checked ? "bg-primary" : "bg-muted-foreground/30",
      )}
    >
      <span
        className={cn(
          "absolute top-0.5 size-4 rounded-full bg-white transition-transform",
          checked ? "translate-x-4" : "translate-x-0.5",
        )}
      />
    </button>
  );
}

function NotificationTargetList({
  targets,
  presetsReady,
  onAdd,
  onEdit,
}: {
  targets: ReturnType<typeof useQuery<NotificationTarget[]>>;
  presetsReady: boolean;
  onAdd: () => void;
  onEdit: (target: NotificationTarget) => void;
}) {
  const qc = useQueryClient();
  const toggle = useMutation({
    mutationFn: (target: NotificationTarget) =>
      saveNotificationTarget({
        ...targetInput(target),
        id: target.id,
        enabled: !target.enabled,
      }),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: notificationTargetsQuery.queryKey }),
  });
  const remove = useMutation({
    mutationFn: deleteNotificationTarget,
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: notificationTargetsQuery.queryKey }),
  });

  return (
    <>
      <DialogHeader>
        <div className="flex items-center justify-between gap-4 pr-8">
          <DialogTitle>Notifications</DialogTitle>
          <Button size="sm" onClick={onAdd} disabled={!presetsReady}>
            <Plus />
            Add target
          </Button>
        </div>
        <DialogDescription>
          Send payout, health, and weekly template updates to your webhooks.
        </DialogDescription>
      </DialogHeader>

      {targets.isPending && (
        <div className="space-y-2">
          <Skeleton className="h-16 w-full rounded-lg" />
          <Skeleton className="h-16 w-full rounded-lg" />
        </div>
      )}
      {targets.isError && (
        <p className="text-sm text-destructive">
          Couldn&apos;t load notification targets.
        </p>
      )}
      {targets.data?.length === 0 && (
        <div className="rounded-lg border border-dashed p-6 text-center">
          <p className="font-medium">No notification targets yet</p>
          <p className="mt-1 text-sm text-muted-foreground">
            Add Discord, Slack, ntfy, or any HTTP webhook.
          </p>
        </div>
      )}
      {targets.data && targets.data.length > 0 && (
        <div className="divide-y rounded-lg border">
          {targets.data.map((target) => (
            <div key={target.id} className="flex items-center gap-3 p-3">
              <Switch
                checked={target.enabled}
                disabled={toggle.isPending}
                label={`${target.enabled ? "Disable" : "Enable"} ${target.name}`}
                onToggle={() => toggle.mutate(target)}
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate font-medium">{target.name}</span>
                  <span className="rounded-md bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                    {KINDS.find((kind) => kind.value === target.kind)?.label ??
                      target.kind}
                  </span>
                </div>
                <p className="truncate text-xs text-muted-foreground">
                  {enabledEventLabels(target).join(" · ") || "No events selected"}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Edit ${target.name}`}
                onClick={() => onEdit(target)}
              >
                <Pencil />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Delete ${target.name}`}
                disabled={remove.isPending}
                onClick={() => {
                  if (window.confirm(`Delete “${target.name}”?`)) {
                    remove.mutate(target.id);
                  }
                }}
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
      )}
      {(toggle.isError || remove.isError) && (
        <p className="text-sm text-destructive">
          {(toggle.error ?? remove.error)?.message}
        </p>
      )}
    </>
  );
}

function enabledEventLabels(target: NotificationTargetInput): string[] {
  return EVENTS.filter((event) => target[event.field]).map(
    (event) => event.label,
  );
}

type HeaderRow = { name: string; value: string };

function NotificationEditor({
  initial,
  id,
  presets,
  onBack,
  onSaved,
}: {
  initial: NotificationTargetInput;
  id?: number;
  presets: NotificationPresets;
  onBack: () => void;
  onSaved: () => void;
}) {
  const qc = useQueryClient();
  const [form, setForm] = useState(initial);
  const [headers, setHeaders] = useState<HeaderRow[]>(
    Object.entries(initial.headers).map(([name, value]) => ({ name, value })),
  );
  const [bodyTouched, setBodyTouched] = useState(false);
  const [headersTouched, setHeadersTouched] = useState(false);
  const [testEvent, setTestEvent] =
    useState<NotificationEventType>("payout");
  const [promptCopied, setPromptCopied] = useState(false);

  const kindMeta = KINDS.find((kind) => kind.value === form.kind) ?? KINDS[3];

  const currentTarget = (): NotificationTargetInput => ({
    ...form,
    headers: Object.fromEntries(
      headers
        .filter((header) => header.name.trim())
        .map((header) => [header.name.trim(), header.value]),
    ),
  });

  const save = useMutation({
    mutationFn: () => saveNotificationTarget({ ...currentTarget(), id }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: notificationTargetsQuery.queryKey });
      onSaved();
    },
  });
  const test = useMutation({
    mutationFn: () => testNotificationDraft(currentTarget(), testEvent),
  });

  const changeKind = (kind: NotificationKind) => {
    const preset = presets[kind];
    setForm((current) => ({
      ...current,
      kind,
      bodyTemplate:
        !bodyTouched || !current.bodyTemplate.trim()
          ? preset.bodyTemplate
          : current.bodyTemplate,
    }));
    if (!headersTouched || headers.length === 0) {
      setHeaders(
        Object.entries(preset.headers).map(([name, value]) => ({
          name,
          value,
        })),
      );
    }
    test.reset();
  };

  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <DialogHeader>
        <div className="flex items-center gap-2 pr-8">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Back to notification targets"
            onClick={onBack}
          >
            <ChevronLeft />
          </Button>
          <DialogTitle>{id ? "Edit target" : "Add target"}</DialogTitle>
        </div>
        <DialogDescription>
          Pick a service, paste its webhook URL, send yourself a test, then
          turn it on.
        </DialogDescription>
      </DialogHeader>

      <Field label="Name">
        <input
          required
          value={form.name}
          onChange={(event) =>
            setForm((current) => ({
              ...current,
              name: event.target.value,
            }))
          }
          placeholder="Team alerts"
          className={inputClass}
        />
      </Field>

      <div className="grid gap-1.5">
        <span className="text-sm font-medium">Service</span>
        <div
          role="radiogroup"
          className="grid grid-cols-4 gap-1 rounded-lg border p-1"
        >
          {KINDS.map((kind) => {
            const active = form.kind === kind.value;
            return (
              <button
                type="button"
                role="radio"
                aria-checked={active}
                key={kind.value}
                onClick={() => changeKind(kind.value)}
                className={cn(
                  "cursor-pointer rounded-md px-2 py-1.5 text-sm",
                  active
                    ? "bg-muted font-medium"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {kind.label}
              </button>
            );
          })}
        </div>
      </div>

      <Field label="Webhook URL">
        <input
          required
          type="url"
          value={form.url}
          onChange={(event) =>
            setForm((current) => ({ ...current, url: event.target.value }))
          }
          placeholder={kindMeta.urlPlaceholder}
          spellCheck={false}
          className={`${inputClass} font-mono`}
        />
        <p className="text-xs text-muted-foreground">{kindMeta.urlHint}</p>
      </Field>

      <fieldset className="space-y-1.5">
        <legend className="text-sm font-medium">Events</legend>
        <div className="grid gap-2 sm:grid-cols-3">
          {EVENTS.map((event) => {
            const checked = form[event.field];
            return (
              <label
                key={event.value}
                className={cn(
                  "flex cursor-pointer items-center gap-2 rounded-md border p-2.5 text-sm transition-colors",
                  checked ? "border-primary" : "hover:bg-muted/50",
                )}
              >
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={(inputEvent) =>
                    setForm((current) => ({
                      ...current,
                      [event.field]: inputEvent.target.checked,
                    }))
                  }
                  className="size-4 accent-primary"
                />
                {event.label}
              </label>
            );
          })}
        </div>
      </fieldset>

      <details className="group rounded-lg border">
        <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2.5 [&::-webkit-details-marker]:hidden">
          <span className="text-sm font-medium">
            Advanced
            <span className="ml-2 text-xs font-normal text-muted-foreground">
              Body template and headers
            </span>
          </span>
          <ChevronDown className="size-4 text-muted-foreground transition-transform group-open:rotate-180" />
        </summary>
        <div className="space-y-4 border-t p-3">
          <Field label="Body template">
            <textarea
              required
              value={form.bodyTemplate}
              onChange={(event) => {
                setBodyTouched(true);
                setForm((current) => ({
                  ...current,
                  bodyTemplate: event.target.value,
                }));
              }}
              rows={6}
              spellCheck={false}
              className={`${textareaClass} resize-y font-mono text-xs`}
            />
            <p className="text-xs text-muted-foreground">
              Fields: <code>{"{{.Title}}"}</code>,{" "}
              <code>{"{{.Message}}"}</code>, <code>{"{{.Event}}"}</code>,{" "}
              <code>{"{{.OccurredAt}}"}</code>, and <code>.Data</code>. Use{" "}
              <code>{"{{json .Message}}"}</code> inside JSON.
            </p>
          </Field>

          <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 rounded-md border border-dashed p-2.5">
            <p className="min-w-40 flex-1 text-xs text-muted-foreground">
              Want help writing this? Copy a prompt with the payload format and
              your draft, ask any AI assistant, then paste its template back.
            </p>
            <div className="flex gap-1.5">
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={async () => {
                  await navigator.clipboard.writeText(
                    templateHelpPrompt(currentTarget()),
                  );
                  setPromptCopied(true);
                  setTimeout(() => setPromptCopied(false), 2000);
                }}
              >
                {promptCopied ? <Check /> : <Copy />}
                {promptCopied ? "Copied" : "Copy prompt"}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() =>
                  window.open(
                    `https://chatgpt.com/?q=${encodeURIComponent(templateHelpPrompt(currentTarget()))}`,
                    "_blank",
                    "noopener",
                  )
                }
              >
                Open in ChatGPT
                <ArrowUpRight />
              </Button>
            </div>
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">Headers</span>
              <Button
                type="button"
                variant="outline"
                size="xs"
                onClick={() => {
                  setHeadersTouched(true);
                  setHeaders((current) => [
                    ...current,
                    { name: "", value: "" },
                  ]);
                }}
              >
                <Plus />
                Add header
              </Button>
            </div>
            {headers.length === 0 && (
              <p className="text-xs text-muted-foreground">
                Content-Type defaults to application/json.
              </p>
            )}
            {headers.map((header, index) => (
              <div key={index} className="grid grid-cols-[1fr_1.5fr_auto] gap-2">
                <input
                  value={header.name}
                  onChange={(event) => {
                    setHeadersTouched(true);
                    setHeaders((current) =>
                      current.map((item, itemIndex) =>
                        itemIndex === index
                          ? { ...item, name: event.target.value }
                          : item,
                      ),
                    );
                  }}
                  placeholder="Header name"
                  aria-label={`Header ${index + 1} name`}
                  spellCheck={false}
                  className={`${inputClass} font-mono text-xs`}
                />
                <input
                  value={header.value}
                  onChange={(event) => {
                    setHeadersTouched(true);
                    setHeaders((current) =>
                      current.map((item, itemIndex) =>
                        itemIndex === index
                          ? { ...item, value: event.target.value }
                          : item,
                      ),
                    );
                  }}
                  placeholder="Value or template"
                  aria-label={`Header ${index + 1} value`}
                  spellCheck={false}
                  className={`${inputClass} font-mono text-xs`}
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Remove header ${index + 1}`}
                  onClick={() => {
                    setHeadersTouched(true);
                    setHeaders((current) =>
                      current.filter((_, itemIndex) => itemIndex !== index),
                    );
                  }}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>
        </div>
      </details>

      <div className="rounded-lg border">
        <div className="flex flex-wrap items-center justify-between gap-2 p-3">
          <div>
            <span className="block text-sm font-medium">Send a test</span>
            <span className="block text-xs text-muted-foreground">
              Sample data — nothing is saved.
            </span>
          </div>
          <div className="flex gap-2">
            <select
              value={testEvent}
              aria-label="Test event type"
              onChange={(event) => {
                setTestEvent(event.target.value as NotificationEventType);
                test.reset();
              }}
              className={cn(inputClass, "h-8 w-auto")}
            >
              {EVENTS.map((event) => (
                <option key={event.value} value={event.value}>
                  {event.label}
                </option>
              ))}
            </select>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8"
              disabled={test.isPending || !form.url || !form.bodyTemplate}
              onClick={() => test.mutate()}
            >
              {test.isPending && <Loader2 className="animate-spin" />}
              Send test
            </Button>
          </div>
        </div>
        {(test.isSuccess || test.isError) && (
          <p
            className={cn(
              "border-t px-3 py-2 text-sm",
              test.isSuccess ? "text-(--viz-up)" : "text-destructive",
            )}
          >
            {test.isSuccess ? "Test delivered." : test.error?.message}
          </p>
        )}
        <div className="flex items-center justify-between gap-3 border-t p-3">
          <div>
            <span className="block text-sm font-medium">Enabled</span>
            <span className="block text-xs text-muted-foreground">
              {form.enabled
                ? id
                  ? "Delivering events — turn off to pause without deleting."
                  : "Starts delivering as soon as you save."
                : "Off — events won’t be sent to this target."}
            </span>
          </div>
          <Switch
            checked={form.enabled}
            label="Enabled"
            onToggle={() =>
              setForm((current) => ({
                ...current,
                enabled: !current.enabled,
              }))
            }
          />
        </div>
      </div>

      {save.isError && (
        <p className="text-sm text-destructive">{save.error.message}</p>
      )}

      <DialogFooter>
        <Button
          type="button"
          variant="ghost"
          onClick={onBack}
          disabled={save.isPending}
        >
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending && <Loader2 className="animate-spin" />}
          Save target
        </Button>
      </DialogFooter>
    </form>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="grid gap-1.5">
      <span className="text-sm font-medium">{label}</span>
      {children}
    </label>
  );
}
