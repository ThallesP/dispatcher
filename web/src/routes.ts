import { type RouteConfig, index, layout } from "@react-router/dev/routes";

export default [
  layout("routes/protected.tsx", [index("routes/analytics.tsx")]),
] satisfies RouteConfig;
