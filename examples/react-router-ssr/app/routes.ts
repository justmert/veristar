import { index, type RouteConfig, route } from "@react-router/dev/routes";

export default [
  index("routes/home.tsx"),
  route("pricing", "routes/pricing.tsx"),
  route("docs/:slug", "routes/doc.tsx"),
] satisfies RouteConfig;
