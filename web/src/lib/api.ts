let csrfToken = "";
export function setCsrfToken(token: string) {
  csrfToken = token;
}
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}
function url(path: string, brandId?: number) {
  if (!path.startsWith("/") || path.startsWith("//"))
    throw new Error("API path must be relative");
  const result = new URL("/api/v1" + path, location.origin);
  if (brandId) result.searchParams.set("brand_id", String(brandId));
  return result;
}
export async function api<T>(
  path: string,
  options: {
    method?: string;
    body?: unknown;
    brandId?: number;
    signal?: AbortSignal;
  } = {},
): Promise<T> {
  const method = options.method ?? "GET";
  const headers: Record<string, string> = { Accept: "application/json" };
  const form = options.body instanceof FormData;
  const raw = options.body instanceof Blob || form;
  if (options.body !== undefined && !form)
    headers["Content-Type"] =
      options.body instanceof Blob
        ? options.body.type || "text/csv"
        : "application/json";
  if (!["GET", "HEAD"].includes(method.toUpperCase()) && csrfToken)
    headers["X-CSRF-Token"] = csrfToken;
  const response = await fetch(url(path, options.brandId), {
    method,
    headers,
    credentials: "same-origin",
    body:
      options.body === undefined
        ? undefined
        : raw
          ? (options.body as Blob | FormData)
          : JSON.stringify(options.body),
    signal: options.signal,
  });
  const data = await response.json().catch(() => null);
  if (!response.ok)
    throw new ApiError(
      data?.error ?? `Request failed (${response.status})`,
      response.status,
    );
  return data as T;
}
export async function download(
  path: string,
  brandId: number,
  filename: string,
) {
  const response = await fetch(url(path, brandId), {
    credentials: "same-origin",
  });
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw new ApiError(data?.error ?? "Download failed", response.status);
  }
  const href = URL.createObjectURL(await response.blob());
  const a = document.createElement("a");
  a.href = href;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(href), 1000);
}
