import type { NextConfig } from "next";

// Static export: `next build` writes plain files to out/, which the Go gateway serves.
// No Next.js server runs in production, so server-only features don't apply.
const nextConfig: NextConfig = {
  output: "export",
};

export default nextConfig;
