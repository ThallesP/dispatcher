import ky from "ky";

// Trailing slash matters: baseUrl resolves like new URL(), so "/api" would
// drop its own segment when joined. Retries are TanStack Query's job.
export const api = ky.create({ baseUrl: "/api/", retry: 0 });
