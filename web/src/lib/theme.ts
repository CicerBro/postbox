import { useEffect, useSyncExternalStore, type SetStateAction } from "react";

export type ThemePreference = "system" | "light" | "dark";

const storageKey = "postbox-theme";

function readThemePreference(): ThemePreference {
  try {
    const saved = localStorage.getItem(storageKey);
    if (saved === "light" || saved === "dark") return saved;
  } catch {
    // Fall back to the system preference if storage is unavailable.
  }
  return "system";
}

let currentPreference = readThemePreference();
const listeners = new Set<() => void>();

function notifyPreference() {
  for (const listener of listeners) listener();
}

function subscribePreference(listener: () => void) {
  listeners.add(listener);
  const handleStorage = (event: StorageEvent) => {
    if (event.key !== storageKey && event.key !== null) return;
    currentPreference = readThemePreference();
    notifyPreference();
  };
  window.addEventListener("storage", handleStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", handleStorage);
  };
}

function setThemePreference(value: SetStateAction<ThemePreference>) {
  currentPreference =
    typeof value === "function" ? value(currentPreference) : value;
  try {
    localStorage.setItem(storageKey, currentPreference);
  } catch {
    // Share the preference within this tab even if browser storage is unavailable.
  }
  notifyPreference();
}

export function useThemePreference() {
  const preference = useSyncExternalStore(
    subscribePreference,
    () => currentPreference,
    () => "system" as ThemePreference,
  );
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark =
        preference === "dark" || (preference === "system" && media.matches);
      document.documentElement.classList.toggle("dark", dark);
      document.documentElement.style.colorScheme = dark ? "dark" : "light";
    };
    apply();
    if (preference !== "system") return;
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [preference]);
  return [preference, setThemePreference] as const;
}
