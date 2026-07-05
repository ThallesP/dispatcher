import { reactRouter } from "@react-router/dev/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tailwindcss(), reactRouter()],
  resolve: {
    tsconfigPaths: true,
  },
  server: {
    // In dev, forward API calls to the Go server (go run .)
    proxy: {
      "/api": "http://localhost:8090",
    },
  },
  // The SPA index.html generation starts a Vite preview server during build;
  // pin it to IPv4 loopback so it also works in Docker builds without IPv6.
  preview: {
    host: "127.0.0.1",
  },
});
