/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        "ig-gateway": "#3B82F6",
        "ig-redis": "#F97316",
        "ig-observe": "#8B5CF6",
        "ig-service": "#10B981",
        "ev-success": "#10B981",
        "ev-warning": "#F59E0B",
        "ev-error": "#EF4444",
        "ev-system": "#3B82F6",
        "ev-muted": "#6B7280",
        "ig-bg": "#0A0A0B",
        "ig-surface": "#111113",
        "ig-surface-elevated": "#18181B",
        "ig-border": "#1F1F23",
        "text-primary": "#FAFAFA",
        "text-secondary": "#A1A1AA",
        "text-muted": "#71717A"
      },
      borderRadius: {
        sm: "4px",
        md: "6px",
        lg: "8px",
      },
      fontFamily: {
        sans: ["Geist", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "Fira Code", "Consolas", "monospace"],
      },
      boxShadow: {
        panel: "0 0 0 1px rgba(31, 31, 35, 1)",
      },
      keyframes: {
        pulseGlow: {
          "0%, 100%": { opacity: "0.55" },
          "50%": { opacity: "1" },
        },
        marqueeUp: {
          "0%": { transform: "translateY(0)" },
          "100%": { transform: "translateY(-4px)" },
        },
      },
      animation: {
        "pulse-glow": "pulseGlow 2s ease-in-out infinite",
        "marquee-up": "marqueeUp 1.6s ease-in-out infinite alternate",
      },
    },
  },
  plugins: [],
};
