import { ConfirmDialog } from "../components/ui/confirm-dialog";
import { useDateTime } from "../lib/date-time";
import { StatusMessage } from "../components/StatusMessage";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useApp } from "../lib/app-context";
import { api } from "../lib/api";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Plus, Search, Copy, Mail, Trash2, X, ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { SortableTableHead } from "../components/SortableTableHead";
import { CampaignProgress } from "../components/CampaignProgress";
import { type CampaignCounters } from "../lib/campaign-progress";
import {
  StatusBadge,
  isFinishedCampaign,
  isLiveCampaign,
  Campaign,
  ErrorMessage,
  number,
  tableClass,
} from "./campaign-shared";

type CampaignSummary = Pick<
  Campaign,
  "id" | "subject" | "status" | "created_at" | "scheduled_at"
> & {
  excerpt: string;
  finished_at: number | null;
  progress?: CampaignCounters | null;
  stats?: { recipients: number; sent: number; opens_unique: number; clicks_unique: number } | null;
};

export function CampaignsPage({ brandId }: { brandId: number }) {
  const { formatDateTime } = useDateTime();
  const navigate = useNavigate();
  const { session } = useApp();
  const client = useQueryClient();
  const [deleting, setDeleting] = useState<CampaignSummary | null>(null);
  const [notice, setNotice] = useState("");
  const duplicate = useMutation({
    mutationFn: (campaign: CampaignSummary) =>
      api<{ id: number }>(`/campaigns/${campaign.id}/duplicate`, {
        method: "POST",
        brandId,
      }),
    onSuccess: ({ id }) => {
      client.invalidateQueries({ queryKey: ["campaigns", brandId] });
      navigate(`/campaigns/${id}`);
    },
  });
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
  const [searchOpen, setSearchOpen] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const [sort, setSort] = useState("priority"),
    [direction, setDirection] = useState<"asc" | "desc">("desc");
  useEffect(() => {
    setSearch("");
    setSearchOpen(false);
    setFilter("all");
    setOffset(0);
    setSort("priority");
    setDirection("desc");
  }, [brandId]);
  useEffect(() => {
    if (searchOpen) searchRef.current?.focus();
  }, [searchOpen]);
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
    placeholderData: (previousData, previousQuery) => previousQuery?.queryKey[1] === brandId ? previousData : undefined,
    refetchInterval: query => query.state.data?.some(c => isLiveCampaign(c.status)) ? 5000 : 30000,
  });
  const hasLiveCampaign = query.data?.some(c => isLiveCampaign(c.status));
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
  function closeSearch() {
    setSearch("");
    setSearchOpen(false);
    setOffset(0);
  }
  const CampaignSortIcon = sort === "subject"
    ? direction === "asc" ? ArrowUp : ArrowDown
    : ArrowUpDown;
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
      <ErrorMessage error={query.error ?? duplicate.error} />
      <StatusMessage>{notice}</StatusMessage>
      {query.isPending ? (
        <p role="status">Loading campaigns…</p>
      ) : (
        <div className="table-card">
          <div className="relative overflow-auto">
            <table className={`${tableClass} campaigns-table`}>
              <thead>
                <tr>
                  <th aria-sort={sort === "subject" ? (direction === "asc" ? "ascending" : "descending") : "none"}>
                    <div className="campaign-head-controls">
                      <button
                        type="button"
                        onClick={() => sortBy("subject")}
                        className="inline-flex items-center gap-2 rounded-md px-2 py-1 text-left font-medium hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring"
                        aria-label={`Sort by Campaign${sort === "subject" ? `, currently ${direction === "asc" ? "ascending" : "descending"}` : ""}`}
                      >
                        Campaign
                        <CampaignSortIcon size={14} aria-hidden="true" className={sort === "subject" ? "" : "text-muted-foreground"} />
                      </button>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={searchOpen ? "Close campaign search" : "Search campaigns"}
                        aria-expanded={searchOpen}
                        aria-controls={searchOpen ? "campaign-search-input" : undefined}
                        onClick={() => searchOpen ? closeSearch() : setSearchOpen(true)}
                      >
                        {searchOpen ? <X size={16} aria-hidden="true" /> : <Search size={16} aria-hidden="true" />}
                      </Button>
                    </div>
                    {searchOpen && (
                      <Input
                        id="campaign-search-input"
                        ref={searchRef}
                        className="campaign-head-input"
                        aria-label="Search campaigns"
                        placeholder="Search campaigns…"
                        value={search}
                        onChange={(e) => {
                          setSearch(e.target.value);
                          setOffset(0);
                        }}
                        onKeyDown={(e) => {
                          if (e.key === "Escape") closeSearch();
                        }}
                      />
                    )}
                  </th>
                  {sortHeader("Status", "status")}
                  <th>Recipients</th>
                  <th>Sent</th>
                  <th>Opens</th>
                  <th>Clicks</th>
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {query.data?.map((c) => (
                  <tr key={c.id}>
                    <td className="campaign-name-cell">
                      <Link
                        className="row-title font-medium hover:underline"
                        to={`/campaigns/${c.id}${isFinishedCampaign(c.status) || isLiveCampaign(c.status) ? "/report" : ""}`}
                      >
                        {c.subject}
                      </Link>
                      {c.status === "scheduled" && c.scheduled_at != null && (
                        <p className="row-subtitle">Scheduled {formatDateTime(c.scheduled_at)}</p>
                      )}
                      {isLiveCampaign(c.status) && (
                        <CampaignProgress compact stats={c.progress} status={c.status} observedAt={query.dataUpdatedAt} fresh={!query.isError} />
                      )}
                    </td>
                    <td className="campaign-status-cell">
                      <StatusBadge status={c.status} />
                    </td>
                    <td className="tabular-nums">{c.stats ? number(c.stats.recipients) : "—"}</td>
                    <td className="tabular-nums whitespace-nowrap">{c.status === "sent" && c.finished_at != null ? formatDateTime(c.finished_at) : "—"}</td>
                    <td className="tabular-nums">{c.stats ? number(c.stats.opens_unique) : "—"}</td>
                    <td className="tabular-nums">{c.stats ? number(c.stats.clicks_unique) : "—"}</td>
                    <td className="campaign-actions-cell">
                      <div className="campaign-row-actions">
                        <Button
                          variant="ghost"
                          size="icon"
                          disabled={duplicate.isPending}
                          aria-label={`Clone campaign ${c.subject}`}
                          onClick={() => {
                            duplicate.reset();
                            duplicate.mutate(c);
                          }}
                        >
                          <Copy size={16} aria-hidden="true" />
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
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title="Delete campaign?"
        description={
          <>
            Permanently delete “{deleting?.subject}” and its delivery history
            and reports. This cannot be undone. Subscribers and lists will
            remain.
          </>
        }
        error={deletion.error}
        pending={deletion.isPending}
        pendingLabel="Deleting…"
        confirmLabel="Delete campaign"
        confirmDisabled={!deleting}
        onConfirm={() => {
          if (deleting) deletion.mutate(deleting);
        }}
      />
    </div>
  );
}
