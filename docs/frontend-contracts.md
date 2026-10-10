# Frontend collaboration contracts

The frontend lives in `web/`, uses Bun, React, TypeScript, Tailwind CSS v4, shadcn/ui and TanStack Query. Vite outputs committed assets into `internal/web/dist/`. `internal/web.Handler() http.Handler` serves embedded assets and SPA paths, with unknown API paths returning 404. The app runs without Bun or Node at runtime.

Shared client: `web/src/lib/api.ts` exports `api<T>(path, options?)`, where paths start with `/` relative to `/api/v1` and options are `{method?: string, body?: unknown, brandId?: number, signal?: AbortSignal}`. It handles same-origin cookies, JSON, errors and the current CSRF token. Export `setCsrfToken(token)` and `download(path, brandId, filename)` for authenticated CSV downloads. Preserve query parameters when adding `brand_id`.

`web/src/lib/app-context.tsx` exports `useApp()` with `{brandId, brand, brands, session, logout}`. The shell owns brand selection and authenticated session. Session contains `user_id`, `admin`, `csrf_token`, `totp_enabled`, `passkeys_enabled`, `passkeys_available`, `passkey_count`. Pages accept `{brandId: number}` and may also use context. No page requests brand-scoped data until a brand is selected.

Shared components live under `web/src/components/ui/`: Button (default, outline, ghost, destructive variants; default, sm, icon sizes), Input, Textarea, Badge, Card/CardHeader/CardTitle/CardContent, Dialog/DialogContent/DialogHeader/DialogTitle/DialogFooter. Checkbox uses Radix checked/onCheckedChange props. SelectField uses value, onValueChange and options with value/label pairs, including empty filter values. Tables use consistent shared classes. Shared `lib/utils.ts` exports `cn`. Pages provide accessible loading, empty and error states. Do not invent dashboard data.

Ownership and exports:

- Core owns package/build configuration, dependencies, shared client/context/UI components, CSS, router, login/passkey sign-in, Overview and the Go embed handler.
- Campaigns owns `pages/Campaigns.tsx` exporting CampaignsPage and CampaignDetailPage, `pages/CampaignReport.tsx` exporting CampaignReportPage, `pages/Templates.tsx` exporting TemplatesPage, and `components/EmailEditor.tsx`. Routes: `/campaigns`, `/campaigns/new`, `/campaigns/:id`, `/campaigns/:id/report`, `/templates`. Detail/report use React Router `useParams`.
- Subscribers owns `pages/Subscribers.tsx` exporting SubscribersPage and `pages/Segments.tsx` exporting SegmentsPage. Routes: `/subscribers`, `/segments`; `/contacts` redirects for existing bookmarks.
- Settings owns `pages/Settings.tsx` exporting SettingsPage, `pages/Domains.tsx` exporting DomainsPage, `pages/ApiKeys.tsx` exporting ApiKeysPage. Routes: `/settings`, `/domains`, `/api-keys`. Account security is available even without a brand.

Use `/tmp/postbox-ui-api-map.md` plus actual handlers/OpenAPI for exact data contracts. The map includes passkeys, webhook scopes, preflight and suppression rules. Core alone installs packages and writes the lockfile; other agents request dependencies. Campaign editing needs CodeMirror 6, GrapesJS with newsletter preset and its CSS. QR rendering for TOTP should be local.

The design uses neutral white and charcoal surfaces with Amber Minimal-inspired accents, a compact shadcn-admin inset sidebar and workspace switcher, a primary New dropdown and a clear play/pause sending control. System appearance follows the OS. Inputs use normal text weight; checkbox labels have consistent spacing. Charts use restrained amber, blue and teal colors with readable legends. Keep the same shared controls on all pages.

Raw full-document HTML remains authoritative. Sandbox previews. Visual editing must show a proposed-source diff and require explicit Apply before replacing HTML. Real subscriber previews use the Go renderer. Production unknown delivery is terminal and has no resend action. Deliberate test sends are allowed and clearly marked `[Test]`.
