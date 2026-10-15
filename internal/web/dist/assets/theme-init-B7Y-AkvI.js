// Run before the app and stylesheet load so the first paint uses the chosen theme.
(() => {
  let preference = "system";
  try {
    const saved = localStorage.getItem("postbox-theme");
    if (saved === "light" || saved === "dark" || saved === "system")
      preference = saved;
  } catch {
    // The system theme still works when browser storage is unavailable.
  }
  const dark =
    preference === "dark" ||
    (preference === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
})();
