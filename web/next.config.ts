import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Slice 037: emit a self-contained `.next/standalone` server bundle so
  // web.Dockerfile's runtime stage ships only the traced production
  // dependencies (a few MB) instead of the full node_modules tree. The
  // docker-compose self-host bundle runs `node server.js` from that
  // output.
  output: "standalone",
};

export default nextConfig;
