import { useMemo } from "react";
import { ApiReferenceReact, type ApiReferenceConfiguration } from "@scalar/api-reference-react";
import "@scalar/api-reference-react/style.css";

export default function Reference({ csrf }: { csrf: string }) {
  const configuration = useMemo<Partial<ApiReferenceConfiguration>>(() => ({
    url: "/docs/openapi.json",
    servers: [{ url: window.location.origin, description: "This Postbox instance" }],
    theme: "none",
    withDefaultFonts: false,
    telemetry: false,
    persistAuth: false,
    agent: { disabled: true },
    mcp: { disabled: true },
    proxyUrl: "",
    showDeveloperTools: "never",
    hideDarkModeToggle: true,
    customFetch: async (input, init) => {
      const request = new Request(input, init);
      const url = new URL(request.url, window.location.origin);
      if (url.origin !== window.location.origin) throw new Error("API requests must use this Postbox origin.");
      const headers = new Headers(request.headers);
      // Bearer credentials keep their own scope; session writes require CSRF.
      if (!["GET", "HEAD", "OPTIONS"].includes(request.method) && !headers.has("Authorization")) headers.set("X-CSRF-Token", csrf);
      const response = await fetch(new Request(request, { headers, credentials: "same-origin" }));
      if (response.status === 401 && !headers.has("Authorization")) window.location.replace("/login?next=%2Fdocs%2Fapi");
      return response;
    },
  }), [csrf]);
  return <ApiReferenceReact configuration={configuration} />;
}
