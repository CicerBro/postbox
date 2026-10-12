import { useState } from "react";

type SortValue = string | number | null | undefined;

export function useTableSort<K extends string>(initialColumn: K, initialDirection: "asc" | "desc" = "asc") {
  const [column, setColumn] = useState<K>(initialColumn);
  const [direction, setDirection] = useState<"asc" | "desc">(initialDirection);
  function header(key: K) {
    return {
      active: column === key,
      direction,
      onSort: () => {
        setColumn(key);
        setDirection(column === key && direction === "asc" ? "desc" : "asc");
      },
    };
  }
  function sort<T>(rows: readonly T[], value: (row: T, key: K) => SortValue): T[] {
    return [...rows].sort((a, b) => {
      const left = value(a, column);
      const right = value(b, column);
      // Missing values stay last in either direction.
      if (left == null) return right == null ? 0 : 1;
      if (right == null) return -1;
      const comparison =
        typeof left === "number" && typeof right === "number"
          ? left - right
          : String(left).localeCompare(String(right), undefined, {
              numeric: true,
              sensitivity: "base",
            });
      return direction === "asc" ? comparison : -comparison;
    });
  }
  return { header, sort };
}
