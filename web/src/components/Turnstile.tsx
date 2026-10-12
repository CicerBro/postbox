import { useEffect, useRef, useState } from "react";

interface TurnstileAPI {
  render(container: HTMLElement, options: {
    sitekey: string;
    action: string;
    size: "flexible";
    callback: (token: string) => void;
    "expired-callback": () => void;
    "error-callback": () => void;
  }): string;
  remove(id: string): void;
}

declare global {
  interface Window { turnstile?: TurnstileAPI }
}

let scriptPromise: Promise<TurnstileAPI> | undefined;
function loadTurnstile(): Promise<TurnstileAPI> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (scriptPromise) return scriptPromise;
  scriptPromise = new Promise<TurnstileAPI>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
    script.async = true;
    const fail = () => {
      clearTimeout(timeout);
      script.remove();
      reject(new Error("Security verification could not load. Please retry."));
    };
    const timeout = window.setTimeout(fail, 15000);
    script.onerror = fail;
    script.onload = () => {
      if (!window.turnstile) { fail(); return; }
      clearTimeout(timeout);
      resolve(window.turnstile);
    };
    document.head.append(script);
  }).catch(error => { scriptPromise = undefined; throw error; });
  return scriptPromise;
}

export function Turnstile({ siteKey, onToken }: { siteKey: string; onToken: (token: string) => void }) {
  const container = useRef<HTMLDivElement>(null);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    let cancelled = false;
    let widget: string | undefined;
    let api: TurnstileAPI | undefined;
    onToken("");
    setError("");
    loadTurnstile().then(loaded => {
      if (cancelled || !container.current) return;
      api = loaded;
      widget = api.render(container.current, {
        sitekey: siteKey,
        action: "login",
        size: "flexible",
        callback: token => { if (!cancelled) { setError(""); onToken(token); } },
        "expired-callback": () => { if (!cancelled) onToken(""); },
        "error-callback": () => {
          if (!cancelled) { onToken(""); setError("Security verification failed. Please retry."); }
        },
      });
    }).catch(error => { if (!cancelled) setError((error as Error).message); });
    return () => { cancelled = true; if (widget !== undefined) api?.remove(widget); };
  }, [siteKey, onToken, retry]);
  return (
    <div>
      <div ref={container} className="auth-turnstile" aria-label="Security verification" />
      {error && <p role="alert" className="error">{error} <button type="button" className="text-link" onClick={() => setRetry(value => value + 1)}>Retry verification</button></p>}
    </div>
  );
}
