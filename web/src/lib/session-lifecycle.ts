// One boundary for requests, live connections, and identity-scoped editor drafts.
let generation = 0;
let expired = false;
const listeners = new Set<() => void>();
const requests = new Set<AbortController>();
const drafts = new Map<string, unknown>();
let userId: number | undefined;
export function onSessionExpired(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
export function expireSession() {
  if (expired) return;
  expired = true;
  generation++;
  for (const controller of requests) controller.abort();
  requests.clear();
  for (const listener of listeners) listener();
}
export function beginSession(id: number) {
  if (userId !== undefined && userId !== id) drafts.clear();
  if (expired || userId !== id) generation++;
  userId = id;
  expired = false;
}
export function discardSessionDrafts() { drafts.clear(); }
export function rememberDraft(key: string, value: unknown) { drafts.set(key, value); }
export function readDraft<T>(key: string): T | undefined { return drafts.get(key) as T | undefined; }
export function forgetDraft(key: string) { drafts.delete(key); }
export function sessionRequest(path: string, signal?: AbortSignal) {
  const publicAuth = path === "/auth/config" || path === "/auth/login" || path.startsWith("/auth/passkeys/login/") || path === "/auth/session";
  if (expired && !publicAuth) throw new Error("Your session expired. Sign in to continue.");
  const controller = new AbortController();
  const version = generation;
  const abort = () => controller.abort();
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  requests.add(controller);
  return {
    signal: controller.signal,
    unauthorized() {
      if (version !== generation || publicAuth) return false;
      expireSession();
      return true;
    },
    finish() {
      requests.delete(controller);
      signal?.removeEventListener("abort", abort);
      if (version !== generation) throw new DOMException("Session changed", "AbortError");
    },
    cleanup() { requests.delete(controller); signal?.removeEventListener("abort", abort); },
  };
}
