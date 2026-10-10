import { useEffect, useState, lazy, Suspense, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Routes, Route, NavLink, Link, useNavigate } from "react-router-dom";
import {
  Mail,
  LayoutDashboard,
  Send,
  Users,
  FileCode,
  Filter,
  Globe,
  KeyRound,
  Settings,
  Plus,
  ChevronDown,
  Moon,
  Monitor,
  Sun,
  LogOut,
  Menu,
  X,
} from "lucide-react";
import { api, ApiError, setCsrfToken } from "./lib/api";
import { AppContext, type Brand, type Session } from "./lib/app-context";
import { useThemePreference } from "./lib/theme";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  DialogDescription,
} from "./components/ui/dialog";
import { Login } from "./pages/Login";
import { OverviewPage } from "./pages/Overview";
const CampaignsPage = lazy(() =>
  import("./pages/Campaigns").then((m) => ({ default: m.CampaignsPage })),
);
const CampaignDetailPage = lazy(() =>
  import("./pages/Campaigns").then((m) => ({ default: m.CampaignDetailPage })),
);
const CampaignReportPage = lazy(() =>
  import("./pages/CampaignReport").then((m) => ({
    default: m.CampaignReportPage,
  })),
);
const TemplatesPage = lazy(() =>
  import("./pages/Templates").then((m) => ({ default: m.TemplatesPage })),
);
const ContactsPage = lazy(() =>
  import("./pages/Contacts").then((m) => ({ default: m.ContactsPage })),
);
const SegmentsPage = lazy(() =>
  import("./pages/Segments").then((m) => ({ default: m.SegmentsPage })),
);
const SettingsPage = lazy(() =>
  import("./pages/Settings").then((m) => ({ default: m.SettingsPage })),
);
const DomainsPage = lazy(() =>
  import("./pages/Domains").then((m) => ({ default: m.DomainsPage })),
);
const ApiKeysPage = lazy(() =>
  import("./pages/ApiKeys").then((m) => ({ default: m.ApiKeysPage })),
);
const navigation = [
  ["/", "Overview", LayoutDashboard],
  ["/campaigns", "Campaigns", Send],
  ["/contacts", "Contacts / Lists", Users],
  ["/templates", "Templates", FileCode],
  ["/segments", "Segments", Filter],
  ["/domains", "Domains", Globe],
  ["/api-keys", "API keys", KeyRound],
  ["/settings", "Settings", Settings],
] as const;
export default function App() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [selected, setSelected] = useState(() =>
    Number(localStorage.getItem("postbox-brand") || 0),
  );
  const [newBrand, setNewBrand] = useState(false);
  const [brandError, setBrandError] = useState("");
  const [menu, setMenu] = useState(false);
  const [mobile, setMobile] = useState(false);
  const [theme, setTheme] = useThemePreference();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => {
      const s = await api<Session>("/auth/session");
      setCsrfToken(s.csrf_token);
      return s;
    },
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const brands = useQuery({
    queryKey: ["brands"],
    queryFn: ({ signal }) => api<Brand[]>("/brands?limit=500", { signal }),
    enabled: !!session.data,
  });
  const sending = useQuery({
    queryKey: ["sending"],
    queryFn: () => api<{ enabled: boolean }>("/sending"),
    enabled: !!session.data?.admin,
    refetchInterval: 10000,
  });
  const changeSending = useMutation({
    mutationFn: (enabled: boolean) =>
      api("/sending", { method: "POST", body: { enabled } }),
    onSuccess: () => client.invalidateQueries({ queryKey: ["sending"] }),
  });
  const brand = brands.data?.find((b) => b.id === selected) ?? brands.data?.[0];
  const brandId = brand?.id ?? 0;
  useEffect(() => {
    if (brandId) {
      setSelected(brandId);
      localStorage.setItem("postbox-brand", String(brandId));
    }
  }, [brandId]);
  async function logout() {
    try {
      await api("/auth/logout", { method: "POST" });
    } finally {
      setCsrfToken("");
      client.clear();
      navigate("/");
    }
  }
  async function createBrand(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fields = new FormData(e.currentTarget);
    setBrandError("");
    try {
      const created = await api<{ id: number }>("/brands", {
        method: "POST",
        body: {
          name: fields.get("name"),
          from_name: fields.get("from_name"),
          from_email: fields.get("from_email"),
          reply_to: fields.get("reply_to") || "",
        },
      });
      setSelected(created.id);
      await client.invalidateQueries({ queryKey: ["brands"] });
      setNewBrand(false);
    } catch (e) {
      setBrandError((e as Error).message);
    }
  }
  if (session.isPending)
    return (
      <main className="loading-screen" role="status">
        Loading Postbox…
      </main>
    );
  if (!session.data) {
    if (
      session.error instanceof ApiError &&
      ![401, 403].includes(session.error.status)
    )
      return (
        <main className="loading-screen">
          <p role="alert">{session.error.message}</p>
          <Button onClick={() => session.refetch()}>Retry</Button>
        </main>
      );
    return (
      <Login
        onLogin={async () => {
          await client.invalidateQueries({ queryKey: ["session"] });
        }}
      />
    );
  }
  const noBrand = (
    <div className="empty-state">
      <Mail size={36} />
      <h1>
        {session.data.admin ? "Create your first brand" : "No brand access yet"}
      </h1>
      <p className="muted">
        {session.data.admin
          ? "A brand keeps your sender identity, contacts and campaigns together."
          : "Ask your administrator to grant you access to a brand."}
      </p>
      {session.data.admin && (
        <Button onClick={() => setNewBrand(true)}>
          <Plus size={16} /> Create brand
        </Button>
      )}
      <Link className="text-link" to="/settings">
        Manage account security
      </Link>
    </div>
  );
  return (
    <AppContext.Provider
      value={{
        brandId,
        brand,
        brands: brands.data ?? [],
        session: session.data,
        logout,
      }}
    >
      <div className="app-shell">
        <aside className={`sidebar ${mobile ? "sidebar-open" : ""}`}>
          <Link className="brand-logo" to="/">
            <Mail size={25} />
            <span>postbox</span>
          </Link>
          <button
            className="mobile-close"
            aria-label="Close navigation"
            onClick={() => setMobile(false)}
          >
            <X />
          </button>
          <nav aria-label="Main navigation">
            {navigation.map(([path, label, Icon]) => (
              <NavLink
                key={path}
                to={path}
                end={path === "/"}
                onClick={() => setMobile(false)}
                className={({ isActive }) =>
                  `nav-item ${isActive ? "active" : ""}`
                }
              >
                <Icon size={18} />
                {label}
              </NavLink>
            ))}
          </nav>
          <div className="sidebar-bottom">
            {session.data.admin ? (
              <>
                <button
                  className={`sending-pill ${sending.data?.enabled ? "sending-on" : ""}`}
                  disabled={changeSending.isPending || !sending.data}
                  onClick={() => changeSending.mutate(!sending.data?.enabled)}
                  aria-label={
                    sending.data?.enabled
                      ? "Pause all sending"
                      : "Enable sending"
                  }
                >
                  <span />
                  {sending.data
                    ? sending.data.enabled
                      ? "Sending on"
                      : "Sending off"
                    : "Sending status unavailable"}
                </button>
                {(sending.error || changeSending.error) && (
                  <p role="alert" className="error text-xs">
                    {(changeSending.error ?? sending.error)?.message}
                  </p>
                )}
              </>
            ) : (
              <span className="muted text-xs">
                Sending controlled by administrator
              </span>
            )}
            <div className="sidebar-tools">
              <div className="theme-control" role="group" aria-label="Theme">
                {(
                  [
                    ["system", "System theme", Monitor],
                    ["light", "Light theme", Sun],
                    ["dark", "Dark theme", Moon],
                  ] as const
                ).map(([preference, label, Icon]) => (
                  <button
                    key={preference}
                    type="button"
                    className="theme-option"
                    aria-label={label}
                    title={label}
                    aria-pressed={theme === preference}
                    onClick={() => setTheme(preference)}
                  >
                    <Icon size={16} aria-hidden="true" />
                  </button>
                ))}
              </div>
              <Button variant="ghost" onClick={logout}>
                <LogOut size={16} /> Sign out
              </Button>
            </div>
            <small className="muted">Self-hosted newsletter platform</small>
          </div>
        </aside>
        {mobile && (
          <button
            className="mobile-shade"
            aria-label="Close navigation"
            onClick={() => setMobile(false)}
          />
        )}
        <div className="app-workspace">
          <header className="topbar">
            <Button
              className="mobile-menu"
              size="icon"
              variant="ghost"
              aria-label="Open navigation"
              onClick={() => setMobile(true)}
            >
              <Menu />
            </Button>
            <div className="brand-tabs" aria-label="Brands">
              {brands.data?.map((b) => (
                <button
                  key={b.id}
                  className={b.id === brandId ? "selected" : ""}
                  onClick={() => {
                    setSelected(b.id);
                    setMenu(false);
                  }}
                >
                  {b.name}
                </button>
              ))}
              {session.data.admin && (
                <button
                  aria-label="Create brand"
                  title="Create brand"
                  onClick={() => setNewBrand(true)}
                >
                  <Plus size={16} />
                </button>
              )}
            </div>
            <div className="new-menu">
              <Button onClick={() => setMenu(!menu)} aria-expanded={menu}>
                <Plus size={16} /> New <ChevronDown size={14} />
              </Button>
              {menu && (
                <>
                  <button
                    className="menu-dismiss"
                    aria-label="Close new menu"
                    onClick={() => setMenu(false)}
                  />
                  <div className="dropdown" role="menu">
                    {brandId > 0 &&
                      [
                        ["/campaigns/new", "Campaign"],
                        ["/contacts?new=list", "List"],
                        ["/contacts?new=contact", "Contact"],
                        ["/templates?new=1", "Template"],
                        ["/segments?new=1", "Segment"],
                      ].map(([path, label]) => (
                        <Link
                          role="menuitem"
                          key={path}
                          to={path}
                          onClick={() => setMenu(false)}
                        >
                          {label}
                        </Link>
                      ))}
                    {session.data.admin && (
                      <button
                        role="menuitem"
                        onClick={() => {
                          setNewBrand(true);
                          setMenu(false);
                        }}
                      >
                        Brand
                      </button>
                    )}
                  </div>
                </>
              )}
            </div>
          </header>
          <main className="page-content">
            {brands.isPending ? (
              <p role="status">Loading brands…</p>
            ) : brands.isError ? (
              <div role="alert">
                {brands.error.message}{" "}
                <Button variant="outline" onClick={() => brands.refetch()}>
                  Retry
                </Button>
              </div>
            ) : (
              <Suspense fallback={<p role="status">Loading page…</p>}>
                <Routes>
                  <Route
                    path="/settings"
                    element={<SettingsPage brandId={brandId} />}
                  />
                  {brandId > 0 ? (
                    <>
                      <Route
                        path="/"
                        element={<OverviewPage brandId={brandId} />}
                      />
                      <Route
                        path="/overview"
                        element={<OverviewPage brandId={brandId} />}
                      />
                      <Route
                        path="/campaigns"
                        element={<CampaignsPage brandId={brandId} />}
                      />
                      <Route
                        path="/campaigns/new"
                        element={<CampaignDetailPage brandId={brandId} />}
                      />
                      <Route
                        path="/campaigns/:id"
                        element={<CampaignDetailPage brandId={brandId} />}
                      />
                      <Route
                        path="/campaigns/:id/report"
                        element={<CampaignReportPage brandId={brandId} />}
                      />
                      <Route
                        path="/contacts"
                        element={<ContactsPage brandId={brandId} />}
                      />
                      <Route
                        path="/templates"
                        element={<TemplatesPage brandId={brandId} />}
                      />
                      <Route
                        path="/segments"
                        element={<SegmentsPage brandId={brandId} />}
                      />
                      <Route
                        path="/domains"
                        element={<DomainsPage brandId={brandId} />}
                      />
                      <Route
                        path="/api-keys"
                        element={<ApiKeysPage brandId={brandId} />}
                      />
                      <Route
                        path="*"
                        element={
                          <div className="empty-state">
                            <h1>Page not found</h1>
                            <Link to="/">Return to overview</Link>
                          </div>
                        }
                      />
                    </>
                  ) : (
                    <Route path="*" element={noBrand} />
                  )}
                </Routes>
              </Suspense>
            )}
          </main>
        </div>
      </div>
      <Dialog open={newBrand} onOpenChange={setNewBrand}>
        <DialogContent aria-describedby="brand-description">
          <DialogHeader>
            <DialogTitle>Create a brand</DialogTitle>
            <DialogDescription id="brand-description">
              Set the identity your subscribers will see.
            </DialogDescription>
          </DialogHeader>
          <form className="space-y-4" onSubmit={createBrand}>
            <label>
              Brand name
              <Input name="name" required />
            </label>
            <label>
              Sender name
              <Input name="from_name" required />
            </label>
            <label>
              Sender email
              <Input name="from_email" type="email" required />
            </label>
            <label>
              Reply-to email (optional)
              <Input name="reply_to" type="email" />
            </label>
            {brandError && (
              <p role="alert" className="error">
                {brandError}
              </p>
            )}
            <DialogFooter>
              <Button
                variant="outline"
                type="button"
                onClick={() => setNewBrand(false)}
              >
                Cancel
              </Button>
              <Button type="submit">Create brand</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </AppContext.Provider>
  );
}
