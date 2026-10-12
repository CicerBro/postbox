import { useId, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronsUpDown } from "lucide-react";
import { useApp, type Session } from "../../lib/app-context";
import { useDateTime } from "../../lib/date-time";
import { api } from "../../lib/api";
import { useThemePreference, type ThemePreference } from "../../lib/theme";
import { Input } from "../ui/input";

function availableTimezones(current: string) {
  const intl = Intl as typeof Intl & {
    supportedValuesOf?: (key: string) => string[];
  };
  return [
    ...new Set([
      "UTC",
      Intl.DateTimeFormat().resolvedOptions().timeZone,
      current,
      ...(intl.supportedValuesOf?.("timeZone") ?? []),
    ]),
  ]
    .filter(Boolean)
    .sort();
}

export function TimezonePreferences() {
  const { session } = useApp();
  const { timeZone, formatDateTime } = useDateTime();
  const client = useQueryClient();
  const [theme, setTheme] = useThemePreference();
  const id = useId();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [active, setActive] = useState(0);
  const selected = session.timezone ?? "";
  const label = (zone: string) =>
    zone ||
    `Browser timezone (${Intl.DateTimeFormat().resolvedOptions().timeZone})`;
  const options = ["", ...availableTimezones(selected)].filter((zone) =>
    label(zone)
      .replaceAll("_", " ")
      .toLowerCase()
      .includes(search.replaceAll("_", " ").trim().toLowerCase()),
  );
  const save = useMutation({
    mutationFn: (timezone: string) =>
      api<{ timezone: string }>("/auth/preferences", {
        method: "PATCH",
        body: { timezone },
      }),
    onSuccess: ({ timezone }) => {
      client.setQueryData<Session>(["session"], (previous) =>
        previous ? { ...previous, timezone } : previous,
      );
    },
  });
  function choose(zone: string) {
    setOpen(false);
    setSearch("");
    if (zone !== selected) save.mutate(zone);
  }
  return (
    <section className="space-y-4 max-w-xl">
      <div className="space-y-2">
        <label htmlFor={`${id}-appearance`}>Appearance</label>
        <select
          id={`${id}-appearance`}
          value={theme}
          onChange={(event) => setTheme(event.target.value as ThemePreference)}
        >
          <option value="system">Use system setting</option>
          <option value="light">Light</option>
          <option value="dark">Dark</option>
        </select>
        <p className="text-sm muted">
          Appearance is saved in this browser and applies immediately.
        </p>
      </div>
      <p className="muted">
        Your timezone is saved to your account and used for dates, times, and
        graph labels.
      </p>
      <div
        className="relative"
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node)) {
            setOpen(false);
            setSearch("");
          }
        }}
      >
        <label htmlFor={id}>Timezone</label>
        <div className="relative">
          <Input
            id={id}
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={open}
            aria-controls={`${id}-options`}
            aria-activedescendant={
              open && options[active] !== undefined
                ? `${id}-option-${active}`
                : undefined
            }
            autoComplete="off"
            disabled={save.isPending}
            value={open ? search : label(selected)}
            placeholder="Search timezones…"
            onFocus={() => {
              setOpen(true);
              setSearch("");
              setActive(0);
            }}
            onChange={(event) => {
              setSearch(event.target.value);
              setActive(0);
              setOpen(true);
            }}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                setOpen(true);
                const next =
                  event.key === "ArrowDown"
                    ? Math.min(active + 1, options.length - 1)
                    : Math.max(active - 1, 0);
                setActive(next);
                document
                  .getElementById(`${id}-option-${next}`)
                  ?.scrollIntoView({ block: "nearest" });
              } else if (
                event.key === "Enter" &&
                open &&
                options[active] !== undefined
              ) {
                event.preventDefault();
                choose(options[active]);
              } else if (event.key === "Escape") {
                event.preventDefault();
                setOpen(false);
                setSearch("");
              }
            }}
          />
          <ChevronsUpDown
            size={16}
            aria-hidden="true"
            className="pointer-events-none absolute right-3 top-3 text-muted-foreground"
          />
        </div>
        {open && (
          <div
            id={`${id}-options`}
            role="listbox"
            aria-label="Timezones"
            className="absolute z-50 mt-1 max-h-64 w-full overflow-y-auto rounded-md border bg-background p-1 shadow-md"
          >
            {options.map((zone, index) => (
              <div
                key={zone}
                id={`${id}-option-${index}`}
                role="option"
                aria-selected={zone === selected}
                className={`flex cursor-pointer items-center justify-between rounded px-3 py-2 text-sm ${index === active ? "bg-accent" : ""}`}
                onMouseDown={(event) => event.preventDefault()}
                onMouseEnter={() => setActive(index)}
                onClick={() => choose(zone)}
              >
                {label(zone).replaceAll("_", " ")}
                {zone === selected && <Check size={16} aria-hidden="true" />}
              </div>
            ))}
            {options.length === 0 && (
              <p role="status" className="px-3 py-2 muted">
                No matching timezones.
              </p>
            )}
          </div>
        )}
      </div>
      <p className="text-sm muted">
        Current time: {formatDateTime(Date.now())} ({timeZone})
      </p>
      {save.isPending && <p role="status">Saving timezone…</p>}
      {save.isSuccess && <p role="status">Timezone saved.</p>}
      {save.error && (
        <p role="alert" className="error">
          {save.error.message}
        </p>
      )}
    </section>
  );
}
