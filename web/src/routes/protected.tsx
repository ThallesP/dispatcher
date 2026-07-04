import { Link, Outlet, useRevalidator } from "react-router";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { api } from "~/lib/api";
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
            <CardTitle>Dispatcher</CardTitle>
            <CardDescription>
              Template analytics for your Railway workspace
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              className="w-full bg-[#853bce] text-white hover:bg-[#6e31aa] dark:bg-[#a667e4] dark:text-[#13111c] dark:hover:bg-[#853bce] dark:hover:text-white"
              render={<a href="/api/auth/redirect" />}
            >
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

function Header({ user }: { user: User }) {
  const revalidator = useRevalidator();

  const signOut = async () => {
    await api.post("auth/logout", { throwHttpErrors: false });
    revalidator.revalidate();
  };

  return (
    <header className="border-b">
      <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-3">
        <Link to="/" className="font-heading font-semibold">
          Dispatcher
        </Link>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            {user.avatar && (
              <img
                src={user.avatar}
                alt=""
                className="size-6 rounded-full"
              />
            )}
            <span className="text-sm text-muted-foreground" title={user.email}>
              {user.name}
            </span>
          </div>
          <Button variant="ghost" size="sm" onClick={signOut}>
            Sign out
          </Button>
        </div>
      </div>
    </header>
  );
}
