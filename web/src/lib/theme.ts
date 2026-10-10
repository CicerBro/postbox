import { useEffect, useState } from "react";

export type ThemePreference = "system" | "light" | "dark";

function readThemePreference(): ThemePreference {
  try {
    const saved = localStorage.getItem("postbox-theme");
    if (saved === "light" || saved === "dark") return saved;
  } catch {
    // Fall back to the system preference if storage is unavailable.
  }
  return "system";
}

export function useThemePreference() {
  const [preference, setPreference] =
    useState<ThemePreference>(readThemePreference);
  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark =
        preference === "dark" || (preference === "system" && media.matches);
      document.documentElement.classList.toggle("dark", dark);
      document.documentElement.style.colorScheme = dark ? "dark" : "light";
    };
    apply();
    try {
      localStorage.setItem("postbox-theme", preference);
    } catch {
      // Keep the current tab usable even if the preference cannot be saved.
    }
    if (preference !== "system") return;
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [preference]);
  return [preference, setPreference] as const;
}
