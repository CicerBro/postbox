import { SortableTableHead } from "../SortableTableHead";
import { useTableSort } from "../../lib/use-table-sort";
import { useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
import { SelectField } from "../ui/select";
import { Section, Message, Field, useAction, date } from "./Common";
type Job = {
  id: number;
  queue: string;
  attempts: number;
  last_error: string | null;
  created_at: number;
};
export function Jobs() {
  const sorting = useTableSort<
    "id" | "queue" | "attempts" | "created_at" | "last_error"
  >("id");
  const [queue, setQueue] = useState("");
  const [retentionDays, setRetentionDays] = useState<string>();
  const action = useAction();
  const settings = useQuery({
    queryKey: ["job-settings"],
    queryFn: () => api<{ failed_job_retention_days: number }>("/job-settings"),
  });
  const q = useInfiniteQuery({
    queryKey: ["failed-jobs"],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api<Job[]>(`/jobs?after=${pageParam}&limit=100`),
    getNextPageParam: (rows) =>
      rows.length === 100 ? rows.at(-1)?.id : undefined,
  });
  const rows = q.data?.pages.flat() ?? [];
  const filtered = sorting.sort(
    queue ? rows.filter((r) => r.queue === queue) : rows,
    (row, column) => row[column],
  );
  return (
    <Section title="Failed jobs">
      <p className="text-sm text-muted-foreground">
        Global dead-letter jobs after retries are exhausted. Payloads and
        webhook signing secrets are not displayed. Sorting and queue filtering apply to
        loaded rows.
      </p>
      <form
        className="space-y-3 rounded-lg border p-4"
        onSubmit={(event) => {
          event.preventDefault();
          action.run(async () => {
            await api("/job-settings", {
              method: "PATCH",
              body: { failed_job_retention_days: Number(retentionDays ?? settings.data?.failed_job_retention_days) },
            });
            await settings.refetch();
            setRetentionDays(undefined);
          }, "Failed job retention saved.");
        }}
      >
        <Field
          label="Failed job retention (days)"
          className="w-full"
          type="number"
          min={1}
          max={3650}
          step={1}
          required
          value={retentionDays ?? settings.data?.failed_job_retention_days ?? ""}
          onChange={(event) => setRetentionDays(event.target.value)}
          disabled={!settings.data || action.busy}
          aria-describedby="failed-job-retention-help"
        />
        <p id="failed-job-retention-help" className="text-sm text-muted-foreground">
          Applies to every brand. Hourly housekeeping deletes failed jobs after
          this many days from their final failure. Changes apply on the next run.
        </p>
        <Button disabled={!settings.data || action.busy}>
          {action.busy ? "Saving…" : "Save retention"}
        </Button>
        <Message error={settings.error ?? action.error} message={action.message} />
      </form>
      <div className="toolbar">
        <label htmlFor="jobs-queue" className="block space-y-2 text-sm">
          Queue
          <SelectField
            id="jobs-queue"
            value={queue}
            onValueChange={setQueue}
            options={[
              { value: "", label: "All queues" },
              ...[...new Set(rows.map((r) => r.queue))].map((value) => ({ value, label: value })),
            ]}
          />
        </label>
        <Button
          variant="outline"
          size="sm"
          disabled={q.isFetching}
          onClick={() => q.refetch()}
        >
          Refresh
        </Button>
      </div>
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading failed jobs…</p>
      ) : q.isError ? null : !filtered.length ? (
        <div className="empty-panel">
          <h3>No failed jobs in this view</h3>
          <p className="page-subtitle">
            Jobs that exhaust their retries will appear here.
          </p>
        </div>
      ) : (
        <div className="table-card overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr>
                <SortableTableHead label="Job" {...sorting.header("id")} />
                <SortableTableHead label="Queue" {...sorting.header("queue")} />
                <SortableTableHead label="Attempts" {...sorting.header("attempts")} />
                <SortableTableHead label="Created" {...sorting.header("created_at")} />
                <SortableTableHead label="Error" {...sorting.header("last_error")} />
              </tr>
            </thead>
            <tbody>
              {filtered.map((j) => (
                <tr className="border-t align-top" key={j.id}>
                  <td className="p-3">{j.id}</td>
                  <td className="p-3">{j.queue}</td>
                  <td className="p-3">{j.attempts}</td>
                  <td className="p-3 whitespace-nowrap">
                    {date(j.created_at)}
                  </td>
                  <td className="p-3 max-w-md break-words">
                    {j.last_error || "No error detail"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {q.hasNextPage && (
        <Button
          variant="outline"
          disabled={q.isFetchingNextPage}
          onClick={() => q.fetchNextPage()}
        >
          Load more jobs
        </Button>
      )}
    </Section>
  );
}
