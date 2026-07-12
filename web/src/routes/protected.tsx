import { ChevronDown, LogOut, RefreshCw, TrainFront } from "lucide-react";
import { Link, Outlet, useRevalidator } from "react-router";
import { AutoWithdraw } from "~/components/auto-withdraw-dialog";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { api } from "~/lib/api";
import { useRefreshAnalytics } from "~/queries/analytics";
import { getMe, type User } from "~/queries/me";
import type { Route } from "./+types/protected";

export async function clientLoader() {
  return { user: await getMe() };
}

export default function Protected({ loaderData }: Route.ComponentProps) {
  if (!loaderData.user) {
    return (
      <main className="flex min-h-screen items-center justify-center p-6">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <TrainFront className="size-4 text-primary" />
              Dispatcher
            </CardTitle>
            <CardDescription>
              Template analytics for your Railway workspace
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button className="w-full" render={<a href="/api/auth/redirect" />}>
              Sign in with Railway
            </Button>
          </CardContent>
        </Card>
      </main>
    );
  }
  return (
    <>
      <Header user={loaderData.user} />
      <Outlet context={loaderData.user} />
    </>
  );
}

// RefreshButton collects a fresh template snapshot on demand — mainly for a
// first sign-in, when the hourly collector hasn't produced any data yet.
function RefreshButton() {
  const refresh = useRefreshAnalytics();
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Refresh data"
      title="Refresh data"
      onClick={() => refresh.mutate()}
      disabled={refresh.isPending}
    >
      <RefreshCw className={refresh.isPending ? "animate-spin" : undefined} />
    </Button>
  );
}

function Header({ user }: { user: User }) {
  const revalidator = useRevalidator();

  const signOut = async () => {
    await api.post("auth/logout", { throwHttpErrors: false });
    revalidator.revalidate();
  };

  return (
    <header className="border-b bg-background">
      <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-3">
        <Link to="/" className="flex items-center gap-2 font-heading font-semibold">
          <TrainFront className="size-4 text-primary" />
          Dispatcher
        </Link>
        <div className="flex items-center gap-3">
          <RefreshButton />
          <AutoWithdraw />
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="ghost" size="sm" />}>
              {user.avatar && (
                <img src={user.avatar} alt="" className="size-5 rounded-full" />
              )}
              {user.name}
              <ChevronDown className="text-muted-foreground" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56">
              <DropdownMenuGroup>
                <DropdownMenuLabel>
                  <span className="block text-sm font-medium text-foreground">
                    {user.name}
                  </span>
                  <span className="block font-normal">{user.email}</span>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={signOut}>
                  <LogOut />
                  Sign out
                </DropdownMenuItem>
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </header>
  );
}
