import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      "/hongloumeng": {
        target: "http://localhost:18000",
        changeOrigin: true,
      },
    },
  },
});
