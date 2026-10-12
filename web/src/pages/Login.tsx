import { useEffect, useState, type FormEvent } from "react";
import { Mail, KeyRound, ShieldCheck, ArrowRight } from "lucide-react";
import { api } from "../lib/api";
import type { LoginResponse, TOTPChallengeResponse } from "../lib/generated-contracts";
import { requestOptions, credentialJSON } from "../lib/webauthn";
import { Input } from "../components/ui/input";
import { Button } from "../components/ui/button";
import { Turnstile } from "../components/Turnstile";
export function Login({ onLogin, expired = false }: { onLogin: () => Promise<void>; expired?: boolean }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [totp, setTotp] = useState(false);
  const [totpRequired, setTotpRequired] = useState(false);
  const [authConfig, setAuthConfig] = useState<{ turnstile_required: boolean; turnstile_site_key: string } | null>(null);
  const [configError, setConfigError] = useState("");
  const [configRetry, setConfigRetry] = useState(0);
  const [turnstileToken, setTurnstileToken] = useState("");
  const [challenge, setChallenge] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setConfigError("");
    api<{ turnstile_required: boolean; turnstile_site_key: string }>("/auth/config", { signal: controller.signal }).then(config => {
      if (typeof config.turnstile_required !== "boolean" || typeof config.turnstile_site_key !== "string" || (config.turnstile_required && !config.turnstile_site_key)) {
        throw new Error("Sign-in verification is not configured correctly.");
      }
      setAuthConfig(config);
    }).catch(error => {
      if (!controller.signal.aborted) setConfigError((error as Error).message);
    });
    return () => controller.abort();
  }, [configRetry]);
  const verificationReady = authConfig !== null && (!authConfig.turnstile_required || turnstileToken !== "");
  function resetChallenge() {
    setTurnstileToken("");
    setChallenge(value => value + 1);
  }
  async function login(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (busy || !verificationReady) return;
    const f = new FormData(e.currentTarget);
    setBusy(true);
    setError("");
    try {
      const response = await api<LoginResponse | TOTPChallengeResponse>("/auth/login", {
        method: "POST",
        body: {
          email: f.get("email"),
          password: f.get("password"),
          totp: f.get("totp") || "",
          ...(authConfig?.turnstile_required ? { turnstile_token: turnstileToken } : {}),
        },
      });
      if ("totp_required" in response && response.totp_required) {
        setTotp(true);
        setTotpRequired(true);
        return;
      }
      await onLogin();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      resetChallenge();
      setBusy(false);
    }
  }
  async function passkey() {
    if (busy || !verificationReady) return;
    setBusy(true);
    setError("");
    try {
      const options = await api<{ publicKey: any }>(
        "/auth/passkeys/login/begin",
        { method: "POST", body: authConfig?.turnstile_required ? { turnstile_token: turnstileToken } : undefined },
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
      resetChallenge();
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
        <p className="muted" role={expired ? "alert" : undefined}>{expired ? "Your session ended. Sign in again to continue. Unsaved campaign edits can be restored for the same account and brand. Failed actions have not been retried." : "Welcome back. Enter your details to continue."}</p>
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
                placeholder="Six-digit code"
                required={totpRequired}
                autoFocus
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
          {totpRequired && (
            <p role="status" className="muted">
              Enter the six-digit code from your authenticator app to finish signing in.
            </p>
          )}
          {authConfig?.turnstile_required && <Turnstile key={challenge} siteKey={authConfig.turnstile_site_key} onToken={setTurnstileToken} />}
          {configError && <p role="alert" className="error">{configError} <button type="button" className="text-link" onClick={() => setConfigRetry(value => value + 1)}>Retry verification</button></p>}
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          <Button className="w-full" disabled={busy || !verificationReady}>
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
                disabled={busy || !verificationReady}
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
