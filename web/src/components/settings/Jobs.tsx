import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
import { Section, Message, date } from "./Common";
type Job = {
  id: number;
  queue: string;
  attempts: number;
  last_error: string | null;
  created_at: number;
};
export function Jobs() {
  const [queue, setQueue] = useState("");
  const q = useInfiniteQuery({
    queryKey: ["failed-jobs"],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api<Job[]>(`/jobs?after=${pageParam}&limit=100`),
    getNextPageParam: (rows) =>
      rows.length === 100 ? rows.at(-1)?.id : undefined,
  });
  const rows = q.data?.pages.flat() ?? [];
  const filtered = queue ? rows.filter((r) => r.queue === queue) : rows;
  return (
    <Section title="Failed jobs">
      <p className="text-sm text-muted-foreground">
        Global dead-letter jobs after retries are exhausted. Payloads and
        webhook signing secrets are not displayed. Queue filtering applies to
        loaded rows.
      </p>
      <div className="flex items-center gap-3">
        <label className="text-sm">
          Queue{" "}
          <select
            className="rounded-md border bg-background p-2"
            value={queue}
            onChange={(e) => setQueue(e.target.value)}
          >
            <option value="">All queues</option>
            {[...new Set(rows.map((r) => r.queue))].map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
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
        <p>No failed jobs in this view.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr>
                <th className="p-3">Job</th>
                <th>Queue</th>
                <th>Attempts</th>
                <th>Created</th>
                <th>Error</th>
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
