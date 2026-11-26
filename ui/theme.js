// Theme mode lives in localStorage ("auto", "dark", "light"). index.html
// applies it before first paint; this module changes it afterwards.
const systemDark = matchMedia("(prefers-color-scheme: dark)");

export function themeMode() {
  return document.documentElement.dataset.themeMode || "auto";
}

export function setThemeMode(mode) {
  localStorage.setItem("dots-theme", mode);
  document.documentElement.dataset.themeMode = mode;
  document.documentElement.dataset.theme =
    mode === "auto" ? (systemDark.matches ? "dark" : "light") : mode;
}

systemDark.addEventListener("change", () => {
  if (themeMode() === "auto") setThemeMode("auto");
});
