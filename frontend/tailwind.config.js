/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        app: {
          up: "#22c55e",
          down: "#ef4444",
          degraded: "#eab308",
          pending: "#94a3b8",
          bg: "#0b1220",
          panel: "#111a2e",
        },
      },
    },
  },
  plugins: [],
};
