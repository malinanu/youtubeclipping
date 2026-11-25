import type { NextConfig } from "next";

// In production Caddy routes /api/* straight to the Go API (needed for SSE
// streaming and large media). The rewrite below is a dev-time convenience.
const apiURL = process.env.API_INTERNAL_URL || "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  reactStrictMode: true,
  poweredByHeader: false,
  // gzip buffers the /api/events stream when requests are proxied through Next;
  // compression is handled by the reverse proxy (CloudPanel nginx / Caddy).
  compress: false,
  images: { unoptimized: true },
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${apiURL}/api/:path*` }];
  },
};

export default nextConfig;
