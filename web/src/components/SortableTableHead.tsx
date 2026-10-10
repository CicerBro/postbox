import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";

export function SortableTableHead({
  label,
  active,
  direction,
  onSort,
}: {
  label: string;
  active: boolean;
  direction: "asc" | "desc";
  onSort: () => void;
}) {
  const Icon = active ? (direction === "asc" ? ArrowUp : ArrowDown) : ArrowUpDown;
  return (
    <th aria-sort={active ? (direction === "asc" ? "ascending" : "descending") : "none"}>
      <button
        type="button"
        onClick={onSort}
        className="inline-flex items-center gap-2 rounded-md px-2 py-1 text-left font-medium hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
        aria-label={`Sort by ${label}${active ? `, currently ${direction === "asc" ? "ascending" : "descending"}` : ""}`}
      >
        {label}
        <Icon size={14} aria-hidden="true" className={active ? "" : "text-muted-foreground"} />
      </button>
    </th>
  );
}
