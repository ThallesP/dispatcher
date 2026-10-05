import { useQuery } from "@tanstack/react-query";
import { Check, Copy, Terminal } from "lucide-react";
import { Fragment, useEffect, useRef, useState } from "react";
import { Button } from "~/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import { api } from "~/lib/api";
import { templateAnalyticsQuery } from "~/queries/analytics";

// github.com/…/raw/… redirects to the raw file (curl -L follows it); it is
// just short enough to sit on one line in the dialog.
const INSTALL_COMMAND =
  "curl -fsSL https://github.com/ThallesP/dispatcher/raw/main/install.sh | sh";

/** A one-time login minted for the CLI: single use, valid for ten minutes. */
interface CliLogin {
  token: string;
  expiresAt: string;
}

/**
 * CliDialog is a header button that opens copy-paste setup for dispatcherctl.
 * "Generate login command" sends the browser through Railway's consent, which
 * mints a grant for the CLI alone, and comes back with a one-time token baked
 * into the login command. The token is masked on screen (safe to screen-share)
 * and only the copy button carries it.
 */
export function CliDialog() {
  const [open, setOpen] = useState(false);
  const [login, setLogin] = useState<CliLogin | null>(null);
  const copyLoginRef = useRef<HTMLButtonElement>(null);
  // Already cached by the dashboard; only used to make the example concrete.
  const templates = useQuery(templateAnalyticsQuery);
  const exampleCode =
    templates.data?.templates.find((t) => t.code)?.code ?? "TEMPLATE";

  // Back from the consent round trip: collect the token the server parked in
  // an HttpOnly cookie, then reopen the dialog (after, so it can focus the new
  // command's copy button). Dropping ?cli=ready first also keeps a re-run of
  // this effect from picking up twice.
  useEffect(() => {
    const url = new URL(window.location.href);
    if (url.searchParams.get("cli") !== "ready") return;
    url.searchParams.delete("cli");
    window.history.replaceState(window.history.state, "", url);
    api
      .post("auth/cli/pickup")
      .json<CliLogin>()
      .then(setLogin, () => setLogin(null))
      .finally(() => setOpen(true));
  }, []);

  const loginLive = login != null && new Date(login.expiresAt) > new Date();
  const loginCommand = (token: string) =>
    `dispatcherctl --url ${window.location.origin} login --token ${token}`;

  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        <Terminal />
        CLI
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="gap-5 sm:max-w-xl"
          // Back with a login command, the next move is copying it.
          initialFocus={loginLive ? copyLoginRef : undefined}
        >
          <DialogHeader>
            <DialogTitle>Use Dispatcher from the terminal</DialogTitle>
          </DialogHeader>

          <Step n={1} title="Install">
            <Command text={INSTALL_COMMAND} />
          </Step>
          <Step n={2} title="Sign in">
            {loginLive ? (
              <Command
                text={loginCommand(login.token)}
                display={loginCommand("•".repeat(16))}
                copyRef={copyLoginRef}
              />
            ) : (
              <Button
                size="sm"
                variant="outline"
                render={<a href="/api/auth/cli/issue" />}
              >
                Generate login command
              </Button>
            )}
          </Step>
          <Step n={3} title="Query">
            <Command
              text={[
                "dispatcherctl summary",
                `dispatcherctl projects ${exampleCode} --days 30`,
                "dispatcherctl raw snapshots --days 7",
              ].join("\n")}
            />
          </Step>
        </DialogContent>
      </Dialog>
    </>
  );
}

function Step({
  n,
  title,
  children,
}: {
  n: number;
  title: string;
  children: React.ReactNode;
}) {
  return (
    // min-w-0: DialogContent is a grid, and a grid item won't shrink below
    // its content, so a long command would push past the dialog's edge.
    <div className="flex min-w-0 gap-3">
      <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-secondary text-xs font-medium">
        {n}
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <div className="text-sm leading-5 font-medium">{title}</div>
        {children}
      </div>
    </div>
  );
}

/** A copyable shell snippet. `display` stands in for `text` on screen when the
 * real command carries a secret; the copy button always copies `text`. Long
 * commands wrap rather than scroll, so the whole line stays visible, and only
 * between arguments: each is an inline block, kept whole where it fits (a
 * browser would otherwise break after the hyphens in "--token") and wrapped
 * inside itself only when it is wider than a line, like a long URL. */
function Command({
  text,
  display = text,
  copyRef,
}: {
  text: string;
  display?: string;
  copyRef?: React.Ref<HTMLButtonElement>;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div className="relative">
      <pre className="rounded-md border bg-muted py-2 pr-10 pl-3 font-mono text-xs leading-relaxed whitespace-pre-wrap">
        {display.split("\n").map((line, i) => (
          <div key={i}>
            {line.split(" ").map((arg, j) => (
              <Fragment key={j}>
                {j > 0 && " "}
                <span className="inline-block max-w-full break-all">{arg}</span>
              </Fragment>
            ))}
          </div>
        ))}
      </pre>
      <Button
        ref={copyRef}
        variant="ghost"
        size="icon-xs"
        className="absolute top-1.5 right-1.5"
        aria-label={copied ? "Copied" : "Copy command"}
        title={copied ? "Copied" : "Copy"}
        onClick={copy}
      >
        {copied ? <Check /> : <Copy />}
      </Button>
    </div>
  );
}
