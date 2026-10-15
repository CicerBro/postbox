import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";

const bootstrap = readFileSync(
  new URL("../src/theme-init.js", import.meta.url),
  "utf8",
);
function firstPaint(
  saved: string | null,
  systemDark: boolean,
  storageBlocked = false,
) {
  let dark = false;
  const root = {
    classList: {
      toggle: (name: string, value: boolean) => {
        if (name === "dark") dark = value;
      },
    },
    style: { colorScheme: "" },
  };
  runInNewContext(bootstrap, {
    localStorage: {
      getItem: () => {
        if (storageBlocked) throw new Error("Storage blocked");
        return saved;
      },
    },
    window: { matchMedia: () => ({ matches: systemDark }) },
    document: { documentElement: root },
  });
  return { dark, colorScheme: root.style.colorScheme };
}

describe("theme before first paint", () => {
  test("new installs follow the operating system", () => {
    expect(firstPaint(null, true)).toEqual({ dark: true, colorScheme: "dark" });
    expect(firstPaint(null, false)).toEqual({
      dark: false,
      colorScheme: "light",
    });
  });
  test("existing explicit choices override the operating system", () => {
    expect(firstPaint("light", true)).toEqual({
      dark: false,
      colorScheme: "light",
    });
    expect(firstPaint("dark", false)).toEqual({
      dark: true,
      colorScheme: "dark",
    });
  });
  test("saved System follows either operating system appearance", () => {
    expect(firstPaint("system", true).dark).toBe(true);
    expect(firstPaint("system", false).dark).toBe(false);
  });
  test("invalid or inaccessible storage falls back to the operating system", () => {
    expect(firstPaint("invalid", true).dark).toBe(true);
    expect(firstPaint(null, true, true).dark).toBe(true);
  });
});
