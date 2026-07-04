import { api } from "~/lib/api";

export interface User {
  id: string;
  avatar: string;
  email: string;
  name: string;
  workspaces: {
    id: string;
  }[];
}

/** Current Railway user, or null when the session cookie is missing/expired. */
export async function getMe(): Promise<User | null> {
  const res = await api.get("auth/me", { throwHttpErrors: false });
  if (!res.ok) return null;
  return res.json<User>();
}
