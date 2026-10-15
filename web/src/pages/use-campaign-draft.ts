import { useBrandPermission } from "../lib/use-brand-permission";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useApp } from "../lib/app-context";
import {
  onSessionExpired,
  rememberDraft,
  readDraft,
  forgetDraft,
} from "../lib/session-lifecycle";
import {
  allRows,
  initialHTML,
  type Campaign,
  type Named,
  type Target,
  type Template,
} from "./campaign-shared";

type Draft = {
  subject: string;
  html: string;
  text: string;
  inline: boolean;
  targets: Target[];
};
type RecoveryDraft = Draft & { baseline: string; notice?: string };

export function useCampaignDraft(brandId: number, routeID: number) {
  const navigate = useNavigate(),
    client = useQueryClient(),
    { session } = useApp();
  const canWrite = useBrandPermission(brandId, "write");
  const hasSendPermission = useBrandPermission(brandId, "send");
  const [draftID, setDraftID] = useState(routeID),
    [draft, setDraft] = useState<Draft>({
      subject: "",
      html: initialHTML,
      text: "",
      inline: true,
      targets: [],
    }),
    [baseline, setBaseline] = useState(""),
    [error, setError] = useState<unknown>(null),
    [notice, updateNotice] = useState("");
  const [noticeVariant, setNoticeVariant] = useState<"success" | "info">("success");
  function setNotice(message: string, variant: "success" | "info" = "success") {
    updateNotice(message);
    setNoticeVariant(variant);
  }
  const { subject, html, text, inline, targets } = draft;
  const setSubject = (subject: string) => setDraft((d) => ({ ...d, subject }));
  const setHtml = (html: string) => setDraft((d) => ({ ...d, html }));
  const setText = (text: string) => setDraft((d) => ({ ...d, text }));
  const setInline = (inline: boolean) => setDraft((d) => ({ ...d, inline }));
  const hydrated = useRef(false);
  const recoveryKey = `${session.user_id}:${brandId}:${routeID}`;
  const recovery = useRef(readDraft<RecoveryDraft>(recoveryKey));
  const campaign = useQuery({
    queryKey: ["campaign", brandId, routeID],
    queryFn: () => api<Campaign[]>(`/campaigns/${routeID}`, { brandId }),
    enabled: brandId > 0 && routeID > 0,
    refetchInterval: 5000,
  });
  const targetQuery = useQuery({
    queryKey: ["campaign-targets", brandId, routeID],
    queryFn: () => api<Target[]>(`/campaigns/${routeID}/targets`, { brandId }),
    enabled: brandId > 0 && routeID > 0,
  });
  const choices = useQuery({
    queryKey: ["campaign-choices", brandId],
    queryFn: async () => {
      const [lists, segments, templates] = await Promise.all([
        allRows<Named>("/lists", brandId),
        allRows<Named>("/segments", brandId),
        allRows<Template>("/templates", brandId),
      ]);
      return { lists, segments, templates };
    },
    enabled: brandId > 0,
  });
  const snapshot = JSON.stringify({ subject, html, text, inline, targets });
  const dirty = routeID === 0 ? true : snapshot !== baseline;
  const latestDraft = useRef({ ...draft, baseline, dirty });
  latestDraft.current = { ...draft, baseline, dirty };
  useEffect(
    () =>
      onSessionExpired(() => {
        if (latestDraft.current.dirty)
          rememberDraft(recoveryKey, latestDraft.current);
      }),
    [recoveryKey],
  );
  useEffect(() => {
    if (routeID || !recovery.current) return;
    const d = recovery.current;
    setDraft({
      subject: d.subject,
      html: d.html,
      text: d.text,
      inline: d.inline,
      targets: d.targets,
    });
    setBaseline(d.baseline);
    setNotice(d.notice ?? "Unsaved edits restored. Review and save them when ready.", "info");
    recovery.current = undefined;
    forgetDraft(recoveryKey);
  }, [routeID, recoveryKey]);
  useEffect(() => {
    if (hydrated.current || !campaign.data?.[0] || !targetQuery.data) return;
    const c = campaign.data[0],
      t = targetQuery.data;
    const d = c.status === "draft" ? recovery.current : undefined;
    if (d) {
      setDraft({
        subject: d.subject,
        html: d.html,
        text: d.text,
        inline: d.inline,
        targets: d.targets,
      });
      setBaseline(d.baseline);
      setNotice(d.notice ?? "Unsaved edits restored. Review and save them when ready.", "info");
      hydrated.current = true;
      recovery.current = undefined;
      forgetDraft(recoveryKey);
      return;
    }
    forgetDraft(recoveryKey);
    const saved = {
      subject: c.subject,
      html: c.html,
      text: c.text ?? "",
      inline: Boolean(c.inline_css),
      targets: t,
    };
    setDraft(saved);
    setBaseline(JSON.stringify(saved));
    hydrated.current = true;
  }, [campaign.data, targetQuery.data, recoveryKey]);
  const status = campaign.data?.[0]?.status ?? "draft",
    editable = canWrite && status === "draft",
    owner = campaign.data?.[0]?.created_by;
  const canSend = hasSendPermission && (session.admin || !routeID || owner === session.user_id);
  const save = useMutation({
    mutationFn: async () => {
      if (!subject.trim() || !html.trim())
        throw new Error("Subject and HTML are required.");
      const result = await api<{ id: number }>(
        draftID ? `/campaigns/${draftID}` : "/campaigns",
        {
          method: draftID ? "PATCH" : "POST",
          brandId,
          body: { subject, html, text, inline_css: inline },
        },
      );
      setDraftID(result.id);
      await api(`/campaigns/${result.id}/targets`, {
        method: "PUT",
        brandId,
        body: targets,
      });
      return { id: result.id, basis: snapshot };
    },
    onSuccess: (result) => {
      const saved = result.id;
      forgetDraft(recoveryKey);
      setBaseline(result.basis);
      setNotice("Draft saved.");
      client.invalidateQueries({ queryKey: ["campaigns", brandId] });
      client.invalidateQueries({ queryKey: ["campaign-checklist", brandId] });
      if (!routeID) {
        // The saved route remounts the editor. Carry edits made during either
        // save request across that boundary, retaining what actually reached
        // the server as the baseline for preflight and send checks.
        const { subject, html, text, inline, targets } = latestDraft.current;
        const latest = { subject, html, text, inline, targets };
        if (JSON.stringify(latest) !== result.basis)
          rememberDraft(`${session.user_id}:${brandId}:${saved}`, {
            ...latest,
            baseline: result.basis,
            notice: "Draft saved. Your newer edits are still unsaved.",
          } satisfies RecoveryDraft);
        navigate(`/campaigns/${saved}`, { replace: true });
      }
      else
        client.invalidateQueries({ queryKey: ["campaign", brandId, routeID] });
    },
    onError: setError,
  });
  function targetMode(
    type: "list" | "segment",
    targetID: number,
    mode: Target["mode"] | "",
  ) {
    setDraft((d) => ({
      ...d,
      targets: [
        ...d.targets.filter(
          (t) => t.target_type !== type || t.target_id !== targetID,
        ),
        ...(mode ? [{ target_type: type, target_id: targetID, mode }] : []),
      ],
    }));
  }
  return {
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
  };
}
