import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApp, type Session } from "../../lib/app-context";
import type { PreferencesResponse } from "../../lib/generated-contracts";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
import { Section, Field, Message, useAction } from "./Common";

export function ProfilePreferences() {
  const { session } = useApp();
  const client = useQueryClient();
  const action = useAction();
  const [displayName, setDisplayName] = useState(session.display_name ?? "");
  return (
    <Section
      title="Profile"
      description="Choose how your name appears in the sidebar."
    >
      <p className="text-sm muted">{session.email}</p>
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          void action.run(async () => {
            const saved = await api<PreferencesResponse>("/auth/preferences", {
              method: "PATCH",
              body: { display_name: displayName },
            });
            setDisplayName(saved.display_name);
            client.setQueryData<Session>(["session"], (previous) =>
              previous ? { ...previous, display_name: saved.display_name } : previous,
            );
            await client.invalidateQueries({ queryKey: ["users"] });
          }, "Display name saved.");
        }}
      >
        <Field
          label="Display name"
          autoComplete="nickname"
          maxLength={100}
          value={displayName}
          placeholder={session.email}
          onChange={(event) => setDisplayName(event.target.value)}
        />
        <p className="text-sm muted">Leave blank to use your email address.</p>
        <Button disabled={action.busy}>Save display name</Button>
      </form>
      <Message {...action} />
    </Section>
  );
}
