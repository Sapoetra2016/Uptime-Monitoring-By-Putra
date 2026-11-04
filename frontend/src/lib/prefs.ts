// User preferences persisted in localStorage. These drive UI behaviour across
// the app and are editable from the Settings page ("Tampilan" tab).

export interface Preferences {
  /** UI language. "id" = Bahasa Indonesia, "en" = English. */
  language: "id" | "en";
  /** Default uptime-history window shown on public pages, in days. */
  uptimeWindowDays: number;
  /** Grey out / hide paused monitors in listings. */
  hidePaused: boolean;
  /** Tighter row spacing in tables and cards. */
  compact: boolean;
}

const KEY = "uptime_prefs";

const DEFAULTS: Preferences = {
  language: "id",
  uptimeWindowDays: 30,
  hidePaused: false,
  compact: false,
};

export function getPrefs(): Preferences {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return { ...DEFAULTS };
    return { ...DEFAULTS, ...(JSON.parse(raw) as Partial<Preferences>) };
  } catch {
    return { ...DEFAULTS };
  }
}

export function setPrefs(p: Partial<Preferences>): Preferences {
  const next = { ...getPrefs(), ...p };
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // storage unavailable (private mode etc.) — preferences stay in-memory
  }
  return next;
}
