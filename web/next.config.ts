import type { NextConfig } from "next";

// Static export: `next build` writes plain files to out/, which the Go gateway serves.
// trailingSlash makes /job/ -> job/index.html, which a plain file server can find.
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true,
};

export default nextConfig;
