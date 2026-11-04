// Minimal custom i18n — no external dependencies.
//
// Language lives in Preferences (localStorage) and is shared through a small
// external store so both React components (via useI18n) and plain helpers
// (format.ts, useLive toasts) can translate through the same t() function.
import { useSyncExternalStore } from "react";
import { getPrefs, setPrefs } from "./prefs";
import en from "./locales/en";
import id from "./locales/id";

export type Lang = "id" | "en";

const DICTIONARIES: Record<Lang, Record<string, string>> = { en, id };

// ---- tiny external store --------------------------------------------------
let current: Lang = getPrefs().language;
const listeners = new Set<() => void>();

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function getLanguage(): Lang {
  return current;
}

export function setLanguage(lang: Lang): void {
  if (lang === current) return;
  current = lang;
  setPrefs({ language: lang });
  listeners.forEach((fn) => fn());
}

// ---- translate ------------------------------------------------------------
/**
 * Translate a key with optional `{var}` interpolation. Unknown keys fall back
 * to English, then to the key itself, so a missing entry never blanks the UI.
 */
export function t(key: string, vars?: Record<string, string | number>): string {
  let text = DICTIONARIES[current][key] ?? DICTIONARIES.en[key] ?? key;
  if (vars) {
    for (const [k, v] of Object.entries(vars)) {
      text = text.split(`{${k}}`).join(String(v));
    }
  }
  return text;
}

// ---- React binding --------------------------------------------------------
export function useI18n() {
  const lang = useSyncExternalStore(subscribe, getLanguage, getLanguage);
  return { lang, t, setLanguage };
}
