/// <reference types="vitest" />
import path from "node:path";
import { defineConfig } from "vite";

import pkg from "./package.json";

// https://vitejs.dev/config/
export default defineConfig({
  define: {
    "process.env.NODE_ENV": '"production"',
    __VERSION__: JSON.stringify(pkg.version),
  },
  build: {
    commonjsOptions: {
      transformMixedEsModules: true,
      include: [
        /exponential-backoff/u,
        /google-protobuf/u,
        /@improbable-eng\/grpc-web/u,
        /gen\//u,
      ],
    },
    minify: true,
    target: "esnext",
    lib: {
      entry: "src/main.ts",
      formats: ["es"],
      fileName: "main",
    },
    rollupOptions: {
      onwarn: (warning, warn) => {
        if (warning.code === "EVAL") {
          return;
        }
        warn(warning);
      },
    },
  },
  test: {
    mockReset: true,
  },
});
