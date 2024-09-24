import { defineConfig } from "vite";

import pkg from "./package.json";

// https://vitejs.dev/config/
export default defineConfig({
  define: {
    "process.env.NODE_ENV": '"production"',
    __VERSION__: JSON.stringify(pkg.version),
  },
  build: {
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
