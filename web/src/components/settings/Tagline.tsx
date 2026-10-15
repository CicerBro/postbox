import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type { TaglineSettings } from "../../lib/generated-contracts";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Textarea } from "../ui/textarea";
import { Message, Section, useAction } from "./Common";

export function Tagline() {
  const query = useQuery({
    queryKey: ["tagline-settings"],
    queryFn: () => api<TaglineSettings>("/tagline-settings"),
  });
  return (
    <Section title="Sign-in tagline" description="Show or hide the tagline on the sign-in page, or replace it with your own plain text.">
      <Message error={query.error} />
      {query.isPending ? (
        <p role="status">Loading tagline settings…</p>
      ) : query.isError ? (
        <Button variant="outline" onClick={() => query.refetch()}>Retry tagline settings</Button>
      ) : query.data && <TaglineForm settings={query.data} />}
    </Section>
  );
}

function TaglineForm({ settings }: { settings: TaglineSettings }) {
  const id = useId();
  const [enabled, setEnabled] = useState(settings.enabled);
  const [text, setText] = useState(settings.text);
  const action = useAction();
  const qc = useQueryClient();
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (action.busy) return;
    await action.run(async () => {
      const saved = await api<TaglineSettings>("/tagline-settings", {
        method: "PATCH",
        body: { enabled, text },
      });
      qc.setQueryData(["tagline-settings"], saved);
    }, "Tagline settings saved.");
  }
  return (
    <form onSubmit={save} className="max-w-xl space-y-4">
      <label htmlFor={`${id}-enabled`} className="flex items-center gap-3 text-sm">
        <Switch id={`${id}-enabled`} checked={enabled} onCheckedChange={setEnabled} disabled={action.busy} />
        Show tagline
      </label>
      <label htmlFor={`${id}-text`} className="block space-y-2 text-sm">
        <span>Tagline text</span>
        <Textarea id={`${id}-text`} value={text} onChange={event => setText(event.target.value)} maxLength={500} required={enabled} disabled={action.busy} aria-describedby={`${id}-help`} />
      </label>
      <p id={`${id}-help`} className="text-sm text-muted-foreground">Plain text only, up to 500 characters. HTML and Markdown appear as written. Your text is kept when the tagline is hidden.</p>
      <Button disabled={action.busy || (enabled && !text.trim())}>{action.busy ? "Saving…" : "Save tagline"}</Button>
      <Message error={action.error} message={action.message} />
    </form>
  );
}
