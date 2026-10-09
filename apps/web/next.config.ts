import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  agentRules: false,
  // A self-contained server.js plus the node_modules it needs, for the image.
  output: "standalone",
  poweredByHeader: false,
};

export default nextConfig;
