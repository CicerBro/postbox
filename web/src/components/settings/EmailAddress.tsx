import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApp, type Session } from "../../lib/app-context";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
import { Section, Field, Message, useAction } from "./Common";

export function EmailAddress() {
  const { session } = useApp();
  const client = useQueryClient();
  const action = useAction();
  const [email, setEmail] = useState(session.email ?? "");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  return (
    <Section title="Email address" description="Change the email address you use to sign in.">
      <form className="space-y-4" onSubmit={(event) => {
        event.preventDefault();
        void action.run(async () => {
          const saved = await api<{ email: string }>("/auth/email", {
            method: "PATCH", body: { email, password, code: code || undefined },
          });
          setEmail(saved.email);
          setPassword("");
          setCode("");
          client.setQueryData<Session>(["session"], (previous) =>
            previous ? { ...previous, email: saved.email } : previous,
          );
          await client.invalidateQueries({ queryKey: ["users"] });
        }, "Email updated. Use your new address next time you sign in. Other sessions have been signed out.");
      }}>
        <Field label="Sign-in email" type="email" autoComplete="email" required
          value={email} onChange={(event) => setEmail(event.target.value)} />
        <Field label="Confirm current password" type="password" autoComplete="current-password" required
          value={password} onChange={(event) => setPassword(event.target.value)} />
        {session.totp_enabled && <Field label="Confirm authenticator code" inputMode="numeric"
          autoComplete="one-time-code" required value={code} onChange={(event) => setCode(event.target.value)} />}
        <p className="text-sm text-muted-foreground">Changing your email signs out your other sessions.</p>
        <Button disabled={action.busy || !password || (session.totp_enabled && !code) || email.trim().toLowerCase() === session.email}>
          Save email address
        </Button>
      </form>
      <Message {...action} />
    </Section>
  );
}
