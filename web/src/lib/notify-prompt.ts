import type {
  NotificationEventType,
  NotificationKind,
  NotificationTargetInput,
} from "~/queries/notify";

const EVENT_DOCS: Record<NotificationEventType, string> = {
  payout: `payout — a withdrawal was requested from the Railway balance.
Sample: .Title = "Withdrawal requested"; .Message = "Requested a $123.45 withdrawal from your Railway balance."
.Data fields: .Data.Amount = "$123.45", .Data.AmountCents = 12345, .Data.AccountID = "account_example"`,
  health_drop: `health_drop — a template's health dropped below the 90% threshold.
Sample: .Title = "Template health dropped"; .Message = "Example template health dropped from 96% to 87% (below 90%)."
.Data fields: .Data.TemplateName = "Example template", .Data.TemplateID = "template_example", .Data.Previous = 96, .Data.Current = 87, .Data.Threshold = 90`,
  weekly_summary: `weekly_summary — Monday-morning recap of the last 7 days.
Sample: .Title = "Weekly template summary"; .Message = a ready multi-line recap ("+12 net new projects · +$45.67 payout" plus one line per template).
.Data fields: .Data.From and .Data.To (the week's range), .Data.TotalNetNewProjects = 12, .Data.TotalPayoutDelta = 45.67, and .Data.Templates — a list to {{range}} over where each item has .Name, .NetNewProjects, .PayoutDelta, and .Health (0-100, may be null)`,
};

const KIND_NOTES: Record<NotificationKind, string> = {
  discord:
    'a Discord webhook — the body must be Discord webhook JSON, e.g. {"content": ...} or an embeds payload',
  slack:
    'a Slack incoming webhook — the body must be Slack JSON, e.g. {"text": ...} or Block Kit blocks',
  ntfy: "an ntfy topic — the body is the plain-text message; headers like X-Title, X-Priority, and X-Tags control the rest",
  custom: "my own HTTP endpoint — I decide what the body should look like",
};

// Builds a self-contained prompt the user can paste into any AI assistant to
// get a template written or modified. Deliberately omits the webhook URL and
// header values — those can hold secrets and the prompt may travel via a
// ChatGPT ?q= URL.
export function templateHelpPrompt(target: NotificationTargetInput): string {
  const selected = (
    [
      ["payout", target.onPayout],
      ["health_drop", target.onHealthDrop],
      ["weekly_summary", target.onWeeklySummary],
    ] as const
  )
    .filter(([, on]) => on)
    .map(([event]) => event);
  const events: NotificationEventType[] =
    selected.length > 0
      ? selected
      : ["payout", "health_drop", "weekly_summary"];
  const headerNames = Object.keys(target.headers);

  return `Help me write the body template for a webhook notification in Dispatcher, my Railway analytics dashboard. Dispatcher sends one HTTP POST per event; I control the body template and the request headers.

How templates work:
- The body is a Go text/template rendered against the event. Referencing a field that doesn't exist is an error.
- A json helper is available — {{json .Message}} renders any value as JSON. Use it for every value placed inside a JSON body.
- Header values are Go templates too, and must not contain newlines.
- Content-Type defaults to application/json, and with a JSON content type the rendered body must be valid JSON for every event type below.
- One template handles all my selected event types — branch with {{if eq .Event "payout"}}…{{else}}…{{end}} when they need different layouts. Keep it under 8 KB.

Every event has .Event, .Title, .Message (a ready-to-send summary), .OccurredAt (a Go time.Time), and .Data. Field names are case-sensitive — use them exactly as written.

My selected event types:

${events.map((event) => EVENT_DOCS[event]).join("\n\n")}

My current setup:
- Destination: ${KIND_NOTES[target.kind]}
- Headers set: ${headerNames.length > 0 ? headerNames.join(", ") + " (values omitted here)" : "none — Content-Type defaults to application/json"}
- Current body template:
${target.bodyTemplate.trim() ? "\n" + target.bodyTemplate.trim() : "\n(empty — starting from scratch)"}

First ask me what I want the notification to look like (or what to change about the current template). Then reply with the finished body template in a single code block — plus any headers I should set — so I can paste it straight back into Dispatcher.`;
}
