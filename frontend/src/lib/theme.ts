// Theme store — mirrors the i18n external-store pattern.
//
// The chosen theme lives in Preferences (localStorage) and is applied as a
// `data-theme` attribute on <html>, so global CSS overrides in index.css can
// restyle the entire app without touching individual components.
import { useSyncExternalStore } from "react";
import { getPrefs, setPrefs, type Preferences } from "./prefs";

export type Theme = Preferences["theme"];

// ---- tiny external store --------------------------------------------------
let current: Theme = getPrefs().theme;
const listeners = new Set<() => void>();

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function apply(theme: Theme): void {
  const root = document.documentElement;
  if (theme === "default") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

export function getTheme(): Theme {
  return current;
}

/** Applies the persisted theme before first paint (called from main.tsx). */
export function initTheme(): void {
  current = getPrefs().theme;
  apply(current);
}

export function setTheme(theme: Theme): void {
  if (theme === current) return;
  current = theme;
  setPrefs({ theme });
  apply(theme);
  listeners.forEach((fn) => fn());
}

// ---- React binding --------------------------------------------------------
export function useTheme() {
  const theme = useSyncExternalStore(subscribe, getTheme, getTheme);
  return { theme, setTheme };
}
