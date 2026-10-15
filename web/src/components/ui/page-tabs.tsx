import { useId, useRef, type ReactNode } from "react";
import { Button } from "./button";

type Tab = { value: string; label: string; disabled?: boolean };

export function PageTabs({ value, onValueChange, items, label, children }: {
  value: string;
  onValueChange: (value: string) => void;
  items: Tab[];
  label: string;
  children: ReactNode;
}) {
  const id = useId();
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const enabled = items.filter((item) => !item.disabled);
  const focusable = enabled.find((item) => item.value === value)?.value ?? enabled[0]?.value;
  return (
    <>
      <div className="page-tabs" role="tablist" aria-label={label}>
        {items.map((item) => (
          <Button
            key={item.value}
            ref={(element) => {
              if (element) buttons.current.set(item.value, element);
              else buttons.current.delete(item.value);
            }}
            id={`${id}-tab-${item.value}`}
            type="button"
            role="tab"
            aria-selected={value === item.value}
            aria-controls={`${id}-panel-${item.value}`}
            tabIndex={focusable === item.value ? 0 : -1}
            variant={value === item.value ? "default" : "ghost"}
            size="sm"
            disabled={item.disabled}
            onClick={() => onValueChange(item.value)}
            onKeyDown={(event) => {
              const index = enabled.findIndex((tab) => tab.value === item.value);
              const last = enabled.length - 1;
              const nextIndex = event.key === "Home" ? 0
                : event.key === "End" ? last
                : event.key === "ArrowRight" ? (index + 1) % enabled.length
                : event.key === "ArrowLeft" ? (index + last) % enabled.length
                : undefined;
              if (nextIndex === undefined) return;
              event.preventDefault();
              const next = enabled[nextIndex];
              buttons.current.get(next.value)?.focus();
              onValueChange(next.value);
            }}
          >
            {item.label}
          </Button>
        ))}
      </div>
      <div>
        {items.map((item) => (
          <div
            key={item.value}
            id={`${id}-panel-${item.value}`}
            role="tabpanel"
            aria-labelledby={`${id}-tab-${item.value}`}
            hidden={value !== item.value}
            tabIndex={0}
            className="space-y-4"
          >
            {value === item.value ? children : null}
          </div>
        ))}
      </div>
    </>
  );
}
