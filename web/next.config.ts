import type { NextConfig } from "next";

// In production Caddy routes /api/* straight to the Go API (needed for SSE
// streaming and large media). The rewrite below is a dev-time convenience.
const apiURL = process.env.API_INTERNAL_URL || "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  images: { unoptimized: true },
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiURL}/api/:path*` }];
  },
};

export default nextConfig;
