import { Mail, Users } from "lucide-react";
import { Input } from "../components/ui/input";
import { Checkbox } from "../components/ui/checkbox";
import { Button } from "../components/ui/button";
import { SelectField } from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { EmailEditor } from "../components/EmailEditor";
import type { Named, Target, Template } from "./campaign-shared";

type ContentProps = {
  brandId: number;
  draftID: number;
  canWrite: boolean;
  editable: boolean;
  subject: string;
  html: string;
  text: string;
  inline: boolean;
  targets: Target[];
  templates: Template[];
  setSubject: (value: string) => void;
  setHtml: (value: string) => void;
  setText: (value: string) => void;
  setInline: (value: boolean) => void;
};
export function CampaignContent({
  brandId,
  draftID,
  canWrite,
  editable,
  subject,
  html,
  text,
  inline,
  targets,
  templates,
  setSubject,
  setHtml,
  setText,
  setInline,
}: ContentProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Mail size={18} /> Campaign content
        </CardTitle>
        <p className="row-subtitle">
          Craft the message your subscribers will receive.
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        <label className="block text-sm">
          Subject
          <Input
            value={subject}
            disabled={!editable}
            onChange={(e) => setSubject(e.target.value)}
          />
        </label>
        {editable && (
          <label
            htmlFor="campaign-template"
            className="block space-y-2 text-sm"
          >
            Start from a template
            <SelectField
              id="campaign-template"
              value=""
              onValueChange={(value) => {
                const t = templates.find((t) => t.id === Number(value));
                if (t) {
                  setHtml(t.html);
                  setText(t.text ?? "");
                }
              }}
              options={[
                {
                  value: "",
                  label: "Choose a template to replace the current HTML",
                },
                ...templates.map((t) => ({
                  value: String(t.id),
                  label: t.name,
                })),
              ]}
            />
          </label>
        )}
        <label
          htmlFor="campaign-inline-css"
          className="flex items-center gap-3 text-sm"
        >
          <Checkbox
            id="campaign-inline-css"
            checked={inline}
            disabled={!editable}
            onCheckedChange={(checked) => setInline(checked === true)}
          />
          Inline CSS during rendering
        </label>
        <EmailEditor
          brandId={brandId}
          value={html}
          onChange={setHtml}
          readOnly={!editable}
          previewMode={canWrite ? "rendered" : "source"}
          inlineCSS={inline}
          subject={subject}
          campaignId={draftID}
          text={text}
          audienceTargets={targets}
        />
        <details>
          <summary className="cursor-pointer text-sm">
            Plain-text override (optional)
          </summary>
          <Textarea
            aria-label="Plain text"
            value={text}
            disabled={!editable}
            onChange={(e) => setText(e.target.value)}
            className="mt-2"
          />
          <p className="text-sm text-muted-foreground">
            Leave blank to generate text from HTML.
          </p>
        </details>
      </CardContent>
    </Card>
  );
}

type AudienceProps = {
  editable: boolean;
  targets: Target[];
  loading: boolean;
  choices: { lists: Named[]; segments: Named[] };
  targetMode: (
    type: "list" | "segment",
    id: number,
    mode: Target["mode"] | "",
  ) => void;
};
export function CampaignAudience({
  editable,
  targets,
  loading,
  choices,
  targetMode,
}: AudienceProps) {
  const included = targets.filter((t) => t.mode === "include").length;
  const excluded = targets.filter((t) => t.mode === "exclude").length;
  const unavailable = targets.filter(
    (t) =>
      !choices[t.target_type === "list" ? "lists" : "segments"].some(
        (row) => row.id === t.target_id,
      ),
  );
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Users size={18} /> Audience
        </CardTitle>
        <p className="row-subtitle">
          {included} included {included === 1 ? "audience" : "audiences"} ·{" "}
          {excluded} {excluded === 1 ? "exclusion" : "exclusions"}
        </p>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Include lists or segments, then exclude any audience you want to omit.
          Recipients are deduplicated and inactive or suppressed subscribers are
          skipped.
        </p>
        {loading ? (
          <p role="status">Loading audiences…</p>
        ) : (
          <>
            {unavailable.length > 0 && (
              <div className="space-y-2 rounded-md border p-3 text-sm">
                <p className="text-muted-foreground">
                  Some saved audiences are no longer available. Remove them and
                  choose an available list or segment before sending.
                </p>
                {unavailable.map((target) => (
                  <div
                    key={`${target.target_type}:${target.target_id}`}
                    className="flex flex-wrap items-center justify-between gap-3"
                  >
                    <div>
                      <span className="font-medium">
                        Unavailable {target.target_type} (ID {target.target_id})
                      </span>
                      <span className="ml-2 text-muted-foreground">
                        {target.mode === "include" ? "Included" : "Excluded"}
                      </span>
                    </div>
                    <Button
                      variant="outline"
                      disabled={!editable}
                      aria-label={`Remove unavailable ${target.target_type} ${target.target_id}`}
                      onClick={() =>
                        targetMode(target.target_type, target.target_id, "")
                      }
                    >
                      Remove
                    </Button>
                  </div>
                ))}
              </div>
            )}
            <div className="grid gap-6 md:grid-cols-2">
              {(["list", "segment"] as const).map((type) => (
                <div key={type}>
                  <h3 className="mb-3 font-medium">
                    {type === "list" ? "Lists" : "Segments"}
                  </h3>
                  {choices[type === "list" ? "lists" : "segments"].map((row) => (
                    <div
                      key={row.id}
                      className="audience-option mb-2 flex flex-wrap items-center justify-between gap-3 rounded-md border p-3 text-sm"
                    >
                      <span className="font-medium">{row.name}</span>
                      <div className="flex gap-4">
                        {(["include", "exclude"] as const).map((mode) => (
                          <label key={mode} className="flex items-center gap-2">
                            <Checkbox
                              aria-label={`${mode === "include" ? "Include" : "Exclude"} ${row.name}`}
                              disabled={!editable}
                              checked={targets.some(
                                (t) =>
                                  t.target_type === type &&
                                  t.target_id === row.id &&
                                  t.mode === mode,
                              )}
                              onCheckedChange={(checked) =>
                                targetMode(
                                  type,
                                  row.id,
                                  checked === true ? mode : "",
                                )
                              }
                            />
                            {mode === "include" ? "Include" : "Exclude"}
                          </label>
                        ))}
                      </div>
                    </div>
                  ))}
                  {!choices[type === "list" ? "lists" : "segments"].length && (
                    <p className="text-sm text-muted-foreground">
                      No {type === "list" ? "lists" : "segments"} available.
                    </p>
                  )}
                </div>
              ))}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
