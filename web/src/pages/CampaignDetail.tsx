import { useState } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Button } from "../components/ui/button";
import { StatusMessage } from "../components/StatusMessage";
import { ErrorMessage, StatusBadge, isFinishedCampaign } from "./campaign-shared";
import { CampaignContent, CampaignAudience } from "./campaign-content";
import {
  CampaignDelivery,
  CampaignConfirmationDialog,
  type CampaignConfirmation,
} from "./campaign-delivery";
import { useCampaignDraft } from "./use-campaign-draft";
import type { Checklist } from "./campaign-checklist";

export function CampaignDetailPage({ brandId }: { brandId: number }) {
  const { id } = useParams();
  return (
    <CampaignDetail
      key={`${brandId}:${id ?? "new"}`}
      brandId={brandId}
      id={id}
    />
  );
}
function CampaignDetail({ brandId, id }: { brandId: number; id?: string }) {
  const routeID = id && id !== "new" ? Number(id) : 0;
  const navigate = useNavigate(),
    client = useQueryClient();
  const draft = useCampaignDraft(brandId, routeID);
  const {
    draftID,
    subject,
    html,
    text,
    inline,
    targets,
    error,
    notice,
    noticeVariant,
    campaign,
    targetQuery,
    choices,
    dirty,
    status,
    editable,
    canSend,
    save,
    setSubject,
    setHtml,
    setText,
    setInline,
    setError,
    setNotice,
    targetMode,
  } = draft;
  const [schedule, setSchedule] = useState(""),
    [testEmails, setTestEmails] = useState(""),
    [confirm, setConfirm] = useState<CampaignConfirmation>(null),
    [busy, setBusy] = useState(false);
  const [testSendError, setTestSendError] = useState<unknown>(null);
  const [testSendStatus, setTestSendStatus] = useState<
    "idle" | "sending" | "sent"
  >("idle");
  const checklist = useQuery({
    queryKey: ["campaign-checklist", brandId, draftID],
    queryFn: () =>
      api<Checklist>(`/campaigns/${draftID}/checklist`, { brandId }),
    enabled: false,
    retry: false,
  });
  async function action(action: string, body?: unknown) {
    setError(null);
    setNotice("");
    setTestSendError(null);
    setTestSendStatus(action === "test-send" ? "sending" : "idle");
    setBusy(true);
    try {
      const result = await api<{ id?: number }>(
        `/campaigns/${draftID}/${action}`,
        { method: "POST", brandId, body },
      );
      if (action === "duplicate" && result.id) {
        navigate(`/campaigns/${result.id}`);
        return;
      }
      if (action === "test-send") setTestSendStatus("sent");
      else setNotice("Campaign action completed.");
      setConfirm(null);
      await client.invalidateQueries({
        queryKey: ["campaign", brandId, routeID],
      });
      client.invalidateQueries({ queryKey: ["campaigns", brandId] });
    } catch (e) {
      if (action === "test-send") {
        setTestSendError(e);
        setTestSendStatus("idle");
      } else setError(e);
    } finally {
      setBusy(false);
    }
  }
  async function preflight() {
    setError(null);
    if (dirty || !draftID) {
      setError(
        new Error("Save your draft and audience before running preflight."),
      );
      return;
    }
    const result = await checklist.refetch();
    if (result.error) setError(result.error);
  }
  async function sendingConfirmation(mode: "start" | "schedule") {
    setError(null);
    if (dirty) {
      setError(new Error("Save changes before sending."));
      return;
    }
    if (
      mode === "schedule" &&
      (!schedule || new Date(schedule).getTime() <= Date.now())
    ) {
      setError(new Error("Choose a future date and time."));
      return;
    }
    const result = await checklist.refetch();
    if (result.error) {
      setError(result.error);
      return;
    }
    if (!result.data?.checks.ready) {
      setError(new Error("Resolve the preflight errors before sending."));
      return;
    }
    setConfirm(mode);
  }
  async function confirmed() {
    if (confirm === "delete") {
      setBusy(true);
      try {
        await api(`/campaigns/${draftID}`, { method: "DELETE", brandId });
        client.invalidateQueries({ queryKey: ["campaigns", brandId] });
        navigate("/campaigns");
      } catch (e) {
        setError(e);
      } finally {
        setBusy(false);
      }
    } else if (confirm === "schedule")
      await action("schedule", {
        scheduled_at: new Date(schedule).getTime(),
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      });
    else if (confirm) await action(confirm);
  }
  if (routeID && (!Number.isSafeInteger(routeID) || routeID < 1))
    return <ErrorMessage error={new Error("Invalid campaign ID")} />;
  if (routeID && isFinishedCampaign(campaign.data?.[0]?.status))
    return <Navigate to={`/campaigns/${routeID}/report`} replace />;
  if (routeID && (campaign.isPending || targetQuery.isPending))
    return <p role="status">Loading campaign…</p>;
  if (routeID && (campaign.error || targetQuery.error))
    return <ErrorMessage error={campaign.error ?? targetQuery.error} />;
  if (routeID && !campaign.data?.length) return <p>Campaign not found.</p>;
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <Link to="/campaigns" className="text-sm underline">
            Campaigns
          </Link>
          <h1 className="mt-2 text-2xl font-bold tracking-tight">
            {routeID ? subject || "Campaign" : "New campaign"}
          </h1>
          <div className="mt-2 flex items-center gap-3">
            <StatusBadge status={status} />
            <span className="row-subtitle">
              {dirty && editable ? "Unsaved changes" : "Saved campaign"}
            </span>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {draftID > 0 && (
            <>
              <Button asChild variant="outline">
                <Link to={`/campaigns/${draftID}/report`}>Report</Link>
              </Button>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => action("duplicate")}
              >
                Duplicate
              </Button>
            </>
          )}
          {editable && (
            <Button
              disabled={save.isPending || busy}
              onClick={() => {
                setError(null);
                setNotice("");
                save.mutate();
              }}
            >
              {save.isPending ? "Saving…" : "Save draft"}
            </Button>
          )}
        </div>
      </div>
      <ErrorMessage error={error ?? choices.error} />
      <StatusMessage variant={noticeVariant}>{notice}</StatusMessage>
      {!canSend && (
        <p className="text-sm text-muted-foreground">
          Duplicate this campaign to send it from your account.
        </p>
      )}
      <CampaignContent
        {...{
          brandId,
          draftID,
          editable,
          subject,
          html,
          text,
          inline,
          targets,
          setSubject,
          setHtml,
          setText,
          setInline,
        }}
        templates={choices.data?.templates ?? []}
      />
      <CampaignAudience
        {...{ editable, targets, targetMode }}
        loading={choices.isPending}
        choices={choices.data ?? { lists: [], segments: [] }}
      />
      {draftID > 0 && (
        <CampaignDelivery
          {...{
            editable,
            busy,
            dirty,
            canSend,
            status,
            schedule,
            testEmails,
            testSendError,
            testSendStatus,
            setSchedule,
            setTestEmails,
            setConfirm,
            preflight,
            sendingConfirmation,
            action,
          }}
          checking={checklist.isFetching}
          checklist={checklist.data}
          checklistError={checklist.error}
        />
      )}
      <CampaignConfirmationDialog
        {...{ confirm, busy, schedule, error, setConfirm, confirmed }}
        checklist={checklist.data}
      />
    </div>
  );
}
