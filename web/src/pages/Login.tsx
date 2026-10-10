import { useState, type FormEvent } from "react";
import { Mail, KeyRound, ShieldCheck, ArrowRight } from "lucide-react";
import { api } from "../lib/api";
import { requestOptions, credentialJSON } from "../lib/webauthn";
import { Input } from "../components/ui/input";
import { Button } from "../components/ui/button";
export function Login({ onLogin }: { onLogin: () => Promise<void> }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [totp, setTotp] = useState(false);
  async function login(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    setBusy(true);
    setError("");
    try {
      await api("/auth/login", {
        method: "POST",
        body: {
          email: f.get("email"),
          password: f.get("password"),
          totp: f.get("totp") || "",
        },
      });
      await onLogin();
    } catch (e) {
      setError((e as Error).message);
      setTotp(true);
    } finally {
      setBusy(false);
    }
  }
  async function passkey() {
    setBusy(true);
    setError("");
    try {
      const options = await api<{ publicKey: any }>(
        "/auth/passkeys/login/begin",
        { method: "POST" },
      );
      const credential = (await navigator.credentials.get({
        publicKey: requestOptions(options.publicKey),
      })) as PublicKeyCredential | null;
      if (!credential) throw new Error("Passkey sign-in was cancelled");
      await api("/auth/passkeys/login/finish", {
        method: "POST",
        body: credentialJSON(credential),
      });
      await onLogin();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login">
      <div className="login-brand">
        <span className="brand-mark">
          <Mail size={21} />
        </span>
        <span>Postbox</span>
      </div>
      <div className="login-card">
        <p className="eyebrow">Your newsletter workspace</p>
        <h1>Sign in to Postbox</h1>
        <p className="muted">Welcome back. Enter your details to continue.</p>
        <form onSubmit={login} className="space-y-5 mt-8">
          <label>
            Email
            <Input
              type="email"
              name="email"
              placeholder="you@example.com"
              autoComplete="username"
              required
              autoFocus
            />
          </label>
          <label>
            Password
            <Input
              type="password"
              name="password"
              placeholder="Enter your password"
              autoComplete="current-password"
              required
            />
          </label>
          {totp ? (
            <label>
              Authenticator code
              <Input
                name="totp"
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                autoComplete="one-time-code"
                placeholder="Six-digit code, if enabled"
              />
            </label>
          ) : (
            <button
              className="text-link"
              type="button"
              onClick={() => setTotp(true)}
            >
              Use an authenticator code
            </button>
          )}
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          <Button className="w-full" disabled={busy}>
            {busy ? (
              "Signing in…"
            ) : (
              <>
                Sign in <ArrowRight size={14} />
              </>
            )}
          </Button>
        </form>
        {window.isSecureContext &&
          typeof PublicKeyCredential !== "undefined" && (
            <>
              <div className="login-divider">Or continue securely</div>
              <Button
                variant="outline"
                className="w-full login-passkey-button"
                disabled={busy}
                onClick={passkey}
              >
                <KeyRound size={16} /> Sign in with a passkey
              </Button>
            </>
          )}
      </div>
      <p className="login-footer">
        <ShieldCheck size={13} /> Self-hosted. Your audience, under your
        control.
      </p>
    </main>
  );
}
