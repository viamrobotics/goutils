import { defineConfig } from "vite";

import pkg from "./package.json";

// https://vitejs.dev/config/
export default defineConfig({
  define: {
    "process.env.NODE_ENV": '"production"',
    __VERSION__: JSON.stringify(pkg.version),
  },
  build: {
    // This config is necessary to transform libraries on the list into ES modules.
    // This can be removed if protobuf-es or a code generating tool that has good
    // support for ES modules is used.
    commonjsOptions: {
      transformMixedEsModules: true,
      include: [/google-protobuf/u, /@improbable-eng\/grpc-web/u, /gen\//u],
    },
    target: "esnext",
    lib: {
      entry: "src/index.ts",
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
});
