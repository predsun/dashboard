// Tailwind config — consumed by the standalone Tailwind CLI (no Node.js needed).
// Class scanning covers Go templates and any inline class usage in handlers.
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./web/templates/**/*.html",
    "./internal/**/*.go",
  ],
  darkMode: "class", // toggled via the theme system; "auto" maps to prefers-color-scheme
  theme: {
    extend: {
      fontFamily: {
        sans: ["Inter", "ui-sans-serif", "system-ui", "sans-serif"],
      },
      transitionDuration: {
        DEFAULT: "200ms",
      },
    },
  },
  plugins: [],
};
