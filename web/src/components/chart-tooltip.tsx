import { useId, useState, type KeyboardEvent, type PointerEvent } from "react";

export function useChartInteraction(count: number, start: number, width: number) {
  const [selected, setSelected] = useState<number | null>(null);
  const id = useId();
  const index = selected === null || !count ? null : Math.min(selected, count - 1);
  const x = index === null
    ? start
    : start + (count === 1 ? width / 2 : index * width / (count - 1));
  function onPointerMove(event: PointerEvent<SVGSVGElement>) {
    if (!count) return;
    const matrix = event.currentTarget.getScreenCTM();
    if (!matrix) return;
    const point = new DOMPoint(event.clientX, event.clientY).matrixTransform(
      matrix.inverse(),
    );
    const nearest = Math.round((point.x - start) / width * (count - 1));
    setSelected(Math.max(0, Math.min(count - 1, nearest)));
  }
  function onKeyDown(event: KeyboardEvent<SVGSVGElement>) {
    if (!count) return;
    if (event.key === "Escape") setSelected(null);
    else if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const next = event.key === "Home"
        ? 0
        : event.key === "End"
          ? count - 1
          : (index ?? 0) + (event.key === "ArrowRight" ? 1 : -1);
      setSelected(Math.max(0, Math.min(count - 1, next)));
    }
  }
  return {
    index,
    x,
    id,
    svgProps: {
      tabIndex: count ? 0 : undefined,
      "aria-describedby": `${id}-instructions`,
      onPointerMove,
      onPointerLeave: () => setSelected(null),
      onFocus: () => count && setSelected(0),
      onBlur: () => setSelected(null),
      onKeyDown,
    },
  };
}

export function ChartInstructions({ id }: { id: string }) {
  return (
    <span id={`${id}-instructions`} className="sr-only">
      Hover to inspect values, or focus the chart and use Left and Right arrow keys.
      Home and End select the first and last point. Escape dismisses the tooltip.
    </span>
  );
}

export function ChartTooltip({ id, position, title, rows }: {
  id: string;
  position: number;
  title: string;
  rows: { label: string; value: number; color: string; detail?: string }[];
}) {
  return (
    <div
      id={`${id}-tooltip`}
      role="status"
      className="pointer-events-none absolute top-2 z-10 w-max max-w-full rounded-lg border border-border bg-[var(--popover)] px-3 py-2 text-xs text-[var(--popover-foreground)] shadow-lg"
      style={{
        left: `${Math.max(2, Math.min(98, position))}%`,
        transform: `translateX(${position > 60 ? "-100%" : position < 40 ? "0" : "-50%"})`,
      }}
    >
      <p className="mb-2 font-medium">{title}</p>
      <div className="space-y-2">
        {rows.map((row) => (
          <div key={row.label}>
            <div className="flex items-center justify-between gap-5">
              <span className="flex items-center gap-2">
                <span className="h-2 w-2 rounded-full" style={{ background: row.color }} />
                {row.label}
              </span>
              <strong className="tabular-nums">{row.value.toLocaleString()}</strong>
            </div>
            {row.detail && (
              <p className="mt-0.5 pl-4 text-muted-foreground">{row.detail}</p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
