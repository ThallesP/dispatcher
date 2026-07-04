import type { Config } from "@react-router/dev/config";

export default {
  appDirectory: "src",
  // SPA mode: build static files into build/client, served by the Go server
  ssr: false,
} satisfies Config;
