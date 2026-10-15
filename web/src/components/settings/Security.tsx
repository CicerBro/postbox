import { InputField } from "../ui/field";
import { useConfirmDialog } from "../ui/confirm-dialog";
import { useState } from "react";
import { ShieldCheck, Fingerprint, Smartphone, Plus } from "lucide-react";
import { Badge } from "../ui/badge";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { QRCodeSVG } from "qrcode.react";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { creationOptions, credentialJSON } from "../../lib/webauthn";
import { Button } from "../ui/button";
import { Section, Message, useAction, date } from "./Common";
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
  const { requestConfirmation, closeConfirmation, confirmationDialog } =
    useConfirmDialog({ pending: a.busy, error: a.error });
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
    <Section
      title="Sign-in protection"
      description="Add another layer of protection to your Postbox account."
    >
      <div className="grid gap-3">
        <div className="flex items-center gap-3 rounded-xl border bg-muted/40 p-4">
          <span className="avatar">
            <Smartphone size={19} aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="row-title">Authenticator app</p>
            <p className="row-subtitle">Time-based verification codes</p>
          </div>
          <Badge variant={session.totp_enabled ? "success" : "warning"}>
            {session.totp_enabled ? "Enabled" : "Not enabled"}
          </Badge>
        </div>
        {session.passkeys_available && (
          <div className="flex items-center gap-3 rounded-xl border bg-muted/40 p-4">
            <span className="avatar">
              <Fingerprint size={19} aria-hidden="true" />
            </span>
            <div className="min-w-0 flex-1">
              <p className="row-title">Passkeys</p>
              <p className="row-subtitle">{session.passkey_count} registered</p>
            </div>
            <Badge variant={session.passkeys_enabled ? "success" : "secondary"}>
              {session.passkeys_enabled ? "Enabled" : "Not enabled"}
            </Badge>
          </div>
        )}
      </div>
      <p className="text-sm text-muted-foreground">
        {session.totp_enabled
          ? "Enter your current password and a code from your authenticator app to change sign-in factors."
          : "Enter your current password to change sign-in factors."}{" "}
        Removing a factor signs out all sessions.
      </p>
      <div className="grid gap-4 xl:grid-cols-2">
        <InputField
          label="Current password"
          type="password"
          autoComplete="new-password"
          data-bwignore="true"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <InputField
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
          className="security-factor-button security-authenticator"
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
          <ShieldCheck size={16} aria-hidden="true" />
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
              className="security-factor-button security-authenticator"
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
          variant="destructive"
          disabled={a.busy || !password || !code}
          onClick={() => {
            requestConfirmation({
              title: "Remove authenticator?",
              description:
                "Remove your authenticator and sign out all sessions?",
              confirmLabel: "Remove authenticator",
              onConfirm: () =>
                a.run(async () => {
                  await api("/auth/totp", {
                    method: "DELETE",
                    body: { password, code },
                  });
                  setPassword("");
                  setCode("");
                  await logout();

                  closeConfirmation();
                }, "Authenticator removed."),
            });
          }}
        >
          Remove authenticator
        </Button>
      )}
      {session.passkeys_available && (
        <div className="space-y-4 border-t pt-4">
          <h3 className="flex items-center gap-2 font-medium">
            <Fingerprint size={18} aria-hidden="true" />
            Passkeys
          </h3>
          <p className="text-sm text-muted-foreground">
            Use your device or a security key for a faster, secure sign-in.
          </p>
          <InputField
            label="New passkey name (optional)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Personal laptop"
          />
          <p
            id="passkey-requirements"
            className="text-sm text-muted-foreground"
          >
            {enrollment
              ? "Finish or cancel authenticator setup before adding a passkey. Then enter your current password and, if your authenticator is enabled, a code in the fields above."
              : session.totp_enabled
                ? "To add a passkey, fill in your current password and authenticator code in the fields above."
                : "To add a passkey, fill in your current password in the field above. No authenticator code is required."}
          </p>
          <Button
            variant="outline"
            className="security-factor-button security-passkey"
            aria-describedby="passkey-requirements"
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
            <Plus size={15} aria-hidden="true" />
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
            <div className="empty-panel">
              <Fingerprint size={24} aria-hidden="true" />
              <p className="page-subtitle">
                No passkeys registered. Add your first device above.
              </p>
            </div>
          ) : (
            <ul className="space-y-3">
              {keys.data.map((k) => (
                <li
                  key={k.id}
                  className="flex flex-wrap justify-between gap-3 rounded-lg border p-3"
                >
                  <div>
                    <p className="row-title">{k.name}</p>
                    <p className="text-xs text-muted-foreground">
                      Added {date(k.created_at)} · Last used{" "}
                      {date(k.last_used_at)}
                    </p>
                  </div>
                  <Button
                    variant="destructive"
                    size="sm"
                    disabled={
                      a.busy || !password || (session.totp_enabled && !code)
                    }
                    onClick={() => {
                      requestConfirmation({
                        title: "Remove passkey?",
                        description: `Remove ${k.name} and sign out all sessions?`,
                        confirmLabel: "Remove passkey",
                        onConfirm: () =>
                          a.run(async () => {
                            await api(`/auth/passkeys/${k.id}`, {
                              method: "DELETE",
                              body: { password, code: code || undefined },
                            });
                            setPassword("");
                            setCode("");
                            await logout();

                            closeConfirmation();
                          }, "Passkey removed."),
                      });
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
      {confirmationDialog}
    </Section>
  );
}
