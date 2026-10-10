import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { QRCodeSVG } from "qrcode.react";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { creationOptions, credentialJSON } from "../../lib/webauthn";
import { Button } from "../ui/button";
import { Section, Field, Message, useAction, date } from "./Common";
type Passkey = {
  id: number;
  name: string;
  created_at: number;
  last_used_at: number | null;
};
export function Security() {
  const { session, logout } = useApp();
  const qc = useQueryClient();
  const a = useAction();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [enrollment, setEnrollment] = useState<{
    secret: string;
    url: string;
    expires: number;
  } | null>(null);
  const keys = useQuery({
    queryKey: ["passkeys"],
    queryFn: () => api<Passkey[]>("/auth/passkeys"),
    enabled: session.passkeys_available,
  });
  async function refresh() {
    setPassword("");
    setCode("");
    await qc.invalidateQueries({ queryKey: ["session"] });
    await qc.invalidateQueries({ queryKey: ["passkeys"] });
  }
  return (
    <Section title="Account security">
      <div className="space-y-1 text-sm">
        <p>
          Authenticator app: {session.totp_enabled ? "Enabled" : "Not enabled"}
        </p>
        {session.passkeys_available && (
          <p>
            Passkeys: {session.passkeys_enabled ? "Enabled" : "Not enabled"} (
            {session.passkey_count} registered)
          </p>
        )}
      </div>
      <p className="text-sm text-muted-foreground">
        Confirm your password to change sign-in factors. Removing a factor signs
        out all sessions.
      </p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Current password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <Field
          label={
            enrollment
              ? "Code from your new authenticator"
              : "Authenticator code"
          }
          value={code}
          inputMode="numeric"
          autoComplete="one-time-code"
          onChange={(e) => setCode(e.target.value)}
        />
      </div>
      {!session.totp_enabled && !enrollment && (
        <Button
          variant="outline"
          disabled={a.busy || !password}
          onClick={() =>
            a.run(async () => {
              const r = await api<{
                secret: string;
                url: string;
                expires_in: number;
              }>("/auth/totp/enroll", { method: "POST", body: { password } });
              setEnrollment({
                ...r,
                expires: Date.now() + r.expires_in * 1000,
              });
              setPassword("");
            }, "Scan the QR code and enter the authenticator code to finish.")
          }
        >
          Set up authenticator
        </Button>
      )}
      {enrollment && (
        <div className="rounded-lg border p-4 space-y-4">
          <p className="text-sm">
            Scan with your authenticator app. Enrollment expires at{" "}
            {date(enrollment.expires)}.
          </p>
          <div className="inline-block bg-white p-3">
            <QRCodeSVG value={enrollment.url} size={180} />
          </div>
          <p className="text-sm break-all">
            Manual setup key: <code>{enrollment.secret}</code>
          </p>
          <div className="flex gap-2">
            <Button
              disabled={a.busy || !code}
              onClick={() =>
                a.run(async () => {
                  await api("/auth/totp/verify", {
                    method: "POST",
                    body: { code },
                  });
                  setEnrollment(null);
                  await refresh();
                }, "Authenticator enabled. Other sessions have been signed out.")
              }
            >
              Verify and enable
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setEnrollment(null);
                setCode("");
              }}
            >
              Cancel setup
            </Button>
          </div>
        </div>
      )}
      {session.totp_enabled && (
        <Button
          variant="outline"
          disabled={a.busy || !password || !code}
          onClick={() => {
            if (
              window.confirm(
                "Remove your authenticator and sign out all sessions?",
              )
            )
              void a.run(async () => {
                await api("/auth/totp", {
                  method: "DELETE",
                  body: { password, code },
                });
                setPassword("");
                setCode("");
                await logout();
              }, "Authenticator removed.");
          }}
        >
          Remove authenticator
        </Button>
      )}
      {session.passkeys_available && (
        <div className="space-y-4 border-t pt-4">
          <h3 className="font-medium">Passkeys</h3>
          <Field
            label="New passkey name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Personal laptop"
          />
          <Button
            variant="outline"
            disabled={
              a.busy ||
              !password ||
              (session.totp_enabled && !code) ||
              enrollment !== null
            }
            onClick={() =>
              a.run(async () => {
                if (!navigator.credentials?.create)
                  throw new Error(
                    "Passkeys are not supported by this browser.",
                  );
                const options = await api<{ publicKey: unknown }>(
                  "/auth/passkeys/register/begin",
                  {
                    method: "POST",
                    body: {
                      password,
                      code: code || undefined,
                      name: name || undefined,
                    },
                  },
                );
                const credential = (await navigator.credentials.create({
                  publicKey: creationOptions(options.publicKey),
                })) as PublicKeyCredential | null;
                if (!credential)
                  throw new Error("Passkey registration was cancelled.");
                await api("/auth/passkeys/register/finish", {
                  method: "POST",
                  body: credentialJSON(credential),
                });
                setName("");
                await refresh();
              }, "Passkey registered.")
            }
          >
            Add passkey
          </Button>
          <Message error={keys.error} />
          {keys.isPending ? (
            <p role="status">Loading passkeys…</p>
          ) : keys.isError ? (
            <Button variant="outline" onClick={() => keys.refetch()}>
              Retry
            </Button>
          ) : !keys.data?.length ? (
            <p className="text-sm">No passkeys registered.</p>
          ) : (
            <ul className="space-y-3">
              {keys.data.map((k) => (
                <li
                  key={k.id}
                  className="flex flex-wrap justify-between gap-3 rounded-lg border p-3"
                >
                  <div>
                    <p className="text-sm font-medium">{k.name}</p>
                    <p className="text-xs text-muted-foreground">
                      Added {date(k.created_at)} · Last used{" "}
                      {date(k.last_used_at)}
                    </p>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={
                      a.busy || !password || (session.totp_enabled && !code)
                    }
                    onClick={() => {
                      if (
                        window.confirm(
                          `Remove ${k.name} and sign out all sessions?`,
                        )
                      )
                        void a.run(async () => {
                          await api(`/auth/passkeys/${k.id}`, {
                            method: "DELETE",
                            body: { password, code: code || undefined },
                          });
                          setPassword("");
                          setCode("");
                          await logout();
                        }, "Passkey removed.");
                    }}
                  >
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      <Message {...a} />
    </Section>
  );
}
