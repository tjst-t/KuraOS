/**
 * Tailwind config for KuraOS admin UI.
 *
 * Design tokens are mirrored from prototype/claude_design/shared/shared.css (Fog
 * palette, jade accent, IBM Plex typography, sidebar/header dimensions). The
 * prototype's CSS class vocabulary (.card, .btn, .tbl, .badge, etc.) lives in
 * ui/src/kura.css under @layer components and is preserved verbatim so the
 * production HTML structure matches the design SSOT.
 */
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./internal/ui/templates/**/*.tmpl",
    "./internal/ui/templates/**/*.html",
  ],
  theme: {
    extend: {
      colors: {
        // Fog palette — cool, low-chroma neutrals.
        fog: {
          0: "oklch(99.2% 0.003 240)",
          50: "oklch(98.0% 0.004 240)",
          100: "oklch(95.8% 0.005 240)",
          200: "oklch(92.0% 0.006 240)",
          300: "oklch(86.0% 0.008 240)",
          400: "oklch(72.0% 0.010 240)",
          500: "oklch(58.0% 0.012 240)",
          600: "oklch(46.0% 0.014 240)",
          700: "oklch(35.0% 0.014 240)",
          800: "oklch(25.0% 0.013 240)",
          850: "oklch(20.0% 0.012 240)",
          900: "oklch(16.0% 0.011 240)",
          950: "oklch(12.0% 0.010 240)",
          1000: "oklch(8.0% 0.008 240)",
        },
        // jade accent — kura/storehouse roof feel. Single accent across the
        // product per DESIGN_PRINCIPLES ui_ux #7.
        accent: {
          DEFAULT: "oklch(62% 0.10 175)",
          soft: "oklch(62% 0.10 175 / 0.12)",
          text: "oklch(50% 0.10 175)",
        },
        // Status colors.
        ok: "oklch(64% 0.12 155)",
        warn: "oklch(72% 0.13 75)",
        crit: "oklch(60% 0.18 25)",
        info: "oklch(62% 0.10 230)",
      },
      fontFamily: {
        sans: ["'IBM Plex Sans'", "-apple-system", "BlinkMacSystemFont", "'Segoe UI'", "sans-serif"],
        mono: ["'IBM Plex Mono'", "'SF Mono'", "Menlo", "Consolas", "monospace"],
      },
      spacing: {
        sidebar: "232px",
        "sidebar-collapsed": "60px",
        header: "56px",
      },
      borderRadius: {
        xs: "4px",
        sm: "6px",
        DEFAULT: "8px",
        lg: "12px",
        xl: "16px",
      },
      maxWidth: {
        content: "1320px",
        "content-narrow": "960px",
      },
    },
  },
  plugins: [],
};
