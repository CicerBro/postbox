import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useApp } from "../lib/app-context";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { api } from "../lib/api";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Plus, Search, ArrowUpRight, Mail, Trash2 } from "lucide-react";
import { SortableTableHead } from "../components/SortableTableHead";
import {
  StatusBadge,
  isFinishedCampaign,
  Campaign,
  ErrorMessage,
  tableClass,
} from "./campaign-shared";

type CampaignSummary = Pick<
  Campaign,
  "id" | "subject" | "status" | "created_at" | "scheduled_at"
> & { excerpt: string };

export function CampaignsPage({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const client = useQueryClient();
  const [deleting, setDeleting] = useState<CampaignSummary | null>(null);
  const [notice, setNotice] = useState("");
  const deletion = useMutation({
    mutationFn: (campaign: CampaignSummary) =>
      api(`/campaigns/${campaign.id}`, { method: "DELETE", brandId }),
    onSuccess: async () => {
      setDeleting(null);
      setNotice("Campaign deleted.");
      setOffset(0);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["campaigns", brandId] }),
        client.invalidateQueries({ queryKey: ["overview", brandId] }),
        client.invalidateQueries({ queryKey: ["campaign-choices", brandId] }),
        client.invalidateQueries({
          queryKey: ["subscribers", brandId, "segment-campaigns"],
        }),
      ]);
    },
  });
  const [offset, setOffset] = useState(0);
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all");
  const [sort, setSort] = useState("priority"),
    [direction, setDirection] = useState<"asc" | "desc">("desc");
  useEffect(() => {
    setSearch("");
    setFilter("all");
    setOffset(0);
    setSort("priority");
    setDirection("desc");
  }, [brandId]);
  const query = useQuery({
    queryKey: ["campaigns", brandId, offset, sort, direction, filter, search],
    queryFn: () => {
      const params = new URLSearchParams({
        offset: String(offset),
        limit: "50",
        sort,
        direction,
        status: filter,
        search,
        summary: "true",
      });
      return api<CampaignSummary[]>(`/campaigns?${params}`, { brandId });
    },
    enabled: brandId > 0,
  });
  function sortBy(column: string) {
    setDirection(sort === column && direction === "asc" ? "desc" : "asc");
    setSort(column);
    setOffset(0);
  }
  function sortHeader(label: string, column: string) {
    return (
      <SortableTableHead
        label={label}
        active={sort === column}
        direction={direction}
        onSort={() => sortBy(column)}
      />
    );
  }
  return (
    <div className="space-y-6">
      <div className="page-header flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Campaigns</h1>
          <p className="page-subtitle text-sm text-muted-foreground">
            Create newsletters and follow each delivery.
          </p>
        </div>
        <Button asChild>
          <Link to="/campaigns/new">
            <Plus size={16} /> New campaign
          </Link>
        </Button>
      </div>
      <div className="page-tabs" aria-label="Filter campaigns">
        {["all", "draft", "scheduled", "sending", "sent"].map((tab) => (
          <button
            key={tab}
            type="button"
            className={filter === tab ? "active" : ""}
            aria-pressed={filter === tab}
            onClick={() => {
              setFilter(tab);
              setOffset(0);
            }}
          >
            {tab === "all"
              ? "All campaigns"
              : tab.charAt(0).toUpperCase() + tab.slice(1)}
          </button>
        ))}
      </div>
      <ErrorMessage error={query.error} />
      {notice && <p role="status">{notice}</p>}
      {query.isPending ? (
        <p role="status">Loading campaigns…</p>
      ) : (
        <div className="table-card">
          <div className="table-toolbar">
            <div>
              <h2 className="section-heading">Campaign overview</h2>
              <p className="row-subtitle">
                {query.data?.length ?? 0} campaigns on this page
              </p>
            </div>
            <div className="toolbar-search">
              <Search size={16} aria-hidden="true" />
              <Input
                aria-label="Search campaigns"
                placeholder="Search campaigns…"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setOffset(0);
                }}
              />
            </div>
          </div>
          <div className="overflow-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  {sortHeader("Campaign", "subject")}
                  {sortHeader("Status", "status")}
                  {sortHeader("Created", "created_at")}
                  {sortHeader("Scheduled", "scheduled_at")}
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {query.data?.map((c) => (
                  <tr key={c.id}>
                    <td>
                      <Link
                        className="row-title font-medium hover:underline"
                        to={`/campaigns/${c.id}${isFinishedCampaign(c.status) ? "/report" : ""}`}
                      >
                        {c.subject}
                      </Link>
                      <p className="row-subtitle max-w-sm truncate">
                        {c.excerpt || `Campaign #${c.id}`}
                      </p>
                    </td>
                    <td>
                      <StatusBadge status={c.status} />
                    </td>
                    <td>{new Date(c.created_at).toLocaleString()}</td>
                    <td>
                      {c.scheduled_at
                        ? new Date(c.scheduled_at).toLocaleString()
                        : "Unscheduled"}
                    </td>
                    <td className="campaign-actions-cell">
                      <div className="campaign-row-actions">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="campaign-report-action"
                          asChild
                        >
                          <Link to={`/campaigns/${c.id}/report`}>
                            Report <ArrowUpRight size={14} />
                          </Link>
                        </Button>
                        {session.admin &&
                          ["draft", "sent", "cancelled"].includes(c.status) && (
                            <Button
                              variant="ghost"
                              size="icon"
                              className="campaign-delete-action"
                              aria-label={`Delete campaign ${c.subject}`}
                              title={`Delete campaign ${c.subject}`}
                              onClick={() => {
                                deletion.reset();
                                setNotice("");
                                setDeleting(c);
                              }}
                            >
                              <Trash2 size={16} aria-hidden="true" />
                            </Button>
                          )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!query.data?.length && (
              <div className="empty-panel">
                <Mail size={28} />
                <h3>No campaigns found</h3>
                <p>
                  {search || filter !== "all"
                    ? "Try another search or status."
                    : "Create your first campaign to reach your audience."}
                </p>
              </div>
            )}
          </div>
        </div>
      )}
      <div className="table-footer flex gap-2">
        <span className="row-subtitle mr-auto">Page {offset / 50 + 1}</span>
        <Button
          variant="outline"
          disabled={offset === 0}
          onClick={() => {
            setOffset(Math.max(0, offset - 50));
          }}
        >
          Previous
        </Button>
        <Button
          variant="outline"
          disabled={query.data?.length !== 50}
          onClick={() => {
            setOffset(offset + 50);
          }}
        >
          Next
        </Button>
      </div>
      <Dialog
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open && !deletion.isPending) setDeleting(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete campaign?</DialogTitle>
            <DialogDescription>
              Permanently delete “{deleting?.subject}” and its delivery history
              and reports. This cannot be undone. Subscribers and lists will
              remain.
            </DialogDescription>
          </DialogHeader>
          <ErrorMessage error={deletion.error} />
          <DialogFooter>
            <Button
              variant="outline"
              disabled={deletion.isPending}
              onClick={() => setDeleting(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deletion.isPending || !deleting}
              onClick={() => {
                if (deleting) deletion.mutate(deleting);
              }}
            >
              {deletion.isPending ? "Deleting…" : "Delete campaign"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
