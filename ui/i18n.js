import en from "./locale-en.js";
import fr from "./locale-fr.js";

// The language lives in localStorage ("auto", "en", "fr"), like the theme. "auto" follows the
// browser. Strings are looked up by key; a key missing from a language falls back to English,
// then to the key itself, so a half-translated page still reads.
const DICTS = { en, fr };
export const LANGS = [["auto", "auto"], ["en", "English"], ["fr", "Français"]];

export function langMode() {
  return localStorage.getItem("dots-lang") || "auto";
}

export function locale() {
  const mode = langMode();
  if (DICTS[mode]) return mode;
  return (navigator.language || "en").toLowerCase().startsWith("fr") ? "fr" : "en";
}

// A language change reloads the page: every string is built once, at render time.
export function setLang(mode) {
  localStorage.setItem("dots-lang", mode);
  location.reload();
}

const current = locale();
document.documentElement.lang = current;

// t("home.title") or t("home.count", [3]) for a "{0}" placeholder.
export function t(key, args = []) {
  const text = DICTS[current][key] ?? en[key] ?? key;
  return args.length ? text.replace(/\{(\d+)\}/g, (_, i) => args[i] ?? "") : text;
}

// Static text of index.html: data-i18n="key" for the content, data-i18n-attr="title:key" for attributes.
export function translatePage(root = document) {
  root.querySelectorAll("[data-i18n]").forEach((el) => { el.textContent = t(el.dataset.i18n); });
  root.querySelectorAll("[data-i18n-attr]").forEach((el) => {
    for (const pair of el.dataset.i18nAttr.split(";")) {
      const [attr, key] = pair.split(":");
      el.setAttribute(attr, t(key));
    }
  });
}

// A status word the server sends ("running", "healthy"): translated when we know it, as is otherwise.
export function ts(word) {
  return DICTS[current][`status.${word}`] ?? word;
}
