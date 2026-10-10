import { useEffect, useState, lazy, Suspense, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Routes,
  Route,
  NavLink,
  Link,
  useNavigate,
  useLocation,
} from "react-router-dom";
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
  PanelLeft,
  ChevronRight,
  ChevronsUpDown,
} from "lucide-react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
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
  const [collapsed, setCollapsed] = useState(
    () => localStorage.getItem("postbox-sidebar") === "collapsed",
  );
  const location = useLocation();
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
  const currentPage =
    navigation.find(([path]) =>
      path === "/"
        ? location.pathname === "/" || location.pathname === "/overview"
        : location.pathname.startsWith(path),
    )?.[1] ?? "Overview";
  const sidebarContent = (
    <>
      <Link className="brand-logo" to="/" onClick={() => setMobile(false)}>
        <span className="brand-mark">
          <Mail size={21} />
        </span>
        <span className="sidebar-label">
          postbox<small>Newsletter workspace</small>
        </span>
      </Link>
      <div className="workspace-identity">
        <span className="workspace-avatar">
          {brand?.name?.slice(0, 1).toUpperCase() ?? "P"}
        </span>
        <span className="sidebar-label">
          <strong>{brand?.name ?? "Your workspace"}</strong>
          <small>Brand workspace</small>
        </span>
      </div>
      <nav aria-label="Main navigation">
        {[
          ["Workspace", navigation.slice(0, 1)],
          ["Manage", navigation.slice(1, 5)],
          ["Configuration", navigation.slice(5)],
        ].map(([heading, items]) => (
          <div className="nav-group" key={heading as string}>
            <p className="nav-group-label sidebar-label">{heading as string}</p>
            {(items as (typeof navigation)[number][]).map(
              ([path, label, Icon]) => (
                <NavLink
                  key={path}
                  to={path}
                  end={path === "/"}
                  title={label}
                  onClick={() => setMobile(false)}
                  className={({ isActive }) =>
                    `nav-item ${isActive ? "active" : ""}`
                  }
                >
                  <Icon size={18} />
                  <span className="sidebar-label">{label}</span>
                </NavLink>
              ),
            )}
          </div>
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
                sending.data?.enabled ? "Pause all sending" : "Enable sending"
              }
              title={
                sending.data?.enabled ? "Pause all sending" : "Enable sending"
              }
            >
              <span className="status-dot" />
              <span className="sidebar-label">
                {sending.data
                  ? sending.data.enabled
                    ? "Sending enabled"
                    : "Sending paused"
                  : "Status unavailable"}
              </span>
            </button>
            {(sending.error || changeSending.error) && (
              <p role="alert" className="error text-xs">
                {(changeSending.error ?? sending.error)?.message}
              </p>
            )}
          </>
        ) : (
          <span className="muted text-xs sidebar-label">
            Sending managed by administrator
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
        </div>
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button className="account-trigger" aria-label="Account menu">
              <span className="avatar">{session.data.admin ? "AD" : "US"}</span>
              <span className="sidebar-label account-copy">
                <strong>
                  {session.data.admin ? "Administrator" : "Team member"}
                </strong>
                <small>Account #{session.data.user_id}</small>
              </span>
              <ChevronsUpDown size={15} className="sidebar-label" />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              className="dropdown-content"
              side="top"
              align="start"
              sideOffset={8}
            >
              <DropdownMenu.Label className="dropdown-label">
                My account
              </DropdownMenu.Label>
              <DropdownMenu.Item asChild>
                <Link to="/settings" onClick={() => setMobile(false)}>
                  <Settings size={15} /> Account & security
                </Link>
              </DropdownMenu.Item>
              <DropdownMenu.Separator className="dropdown-separator" />
              <DropdownMenu.Item onSelect={logout}>
                <LogOut size={15} /> Sign out
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
    </>
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
      <div className={`app-shell ${collapsed ? "sidebar-collapsed" : ""}`}>
        <aside className="sidebar desktop-sidebar">{sidebarContent}</aside>
        <Dialog open={mobile} onOpenChange={setMobile}>
          <DialogContent
            className="navigation-sheet"
            aria-describedby={undefined}
          >
            <DialogTitle className="sr-only">Navigation</DialogTitle>
            <div className="sidebar mobile-sidebar">{sidebarContent}</div>
          </DialogContent>
        </Dialog>
        <div className="app-workspace">
          <header className="topbar">
            <div className="topbar-leading">
              <Button
                className="desktop-toggle"
                size="icon"
                variant="ghost"
                aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
                onClick={() => {
                  setCollapsed(!collapsed);
                  localStorage.setItem(
                    "postbox-sidebar",
                    !collapsed ? "collapsed" : "expanded",
                  );
                }}
              >
                <PanelLeft size={18} />
              </Button>
              <Button
                className="mobile-menu"
                size="icon"
                variant="ghost"
                aria-label="Open navigation"
                onClick={() => setMobile(true)}
              >
                <Menu size={19} />
              </Button>
              <div className="topbar-divider" />
              <nav className="breadcrumbs" aria-label="Breadcrumb">
                <span>Workspace</span>
                <ChevronRight size={14} />
                <strong>{currentPage}</strong>
              </nav>
            </div>
            <div className="topbar-actions">
              <span className="workspace-tag">Self-hosted</span>
              <DropdownMenu.Root>
                <DropdownMenu.Trigger asChild>
                  <Button aria-label="Create new">
                    <Plus size={16} /> New <ChevronDown size={14} />
                  </Button>
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <DropdownMenu.Content
                    className="dropdown-content"
                    align="end"
                    sideOffset={8}
                  >
                    <DropdownMenu.Label className="dropdown-label">
                      Create in {brand?.name ?? "Postbox"}
                    </DropdownMenu.Label>
                    {brandId > 0 &&
                      [
                        ["/campaigns/new", "Campaign", Send],
                        ["/contacts?new=list", "List", Users],
                        ["/contacts?new=contact", "Contact", Users],
                        ["/templates?new=1", "Template", FileCode],
                        ["/segments?new=1", "Segment", Filter],
                      ].map(([path, label, Icon]) => {
                        const ItemIcon = Icon as typeof Send;
                        return (
                          <DropdownMenu.Item asChild key={path as string}>
                            <Link to={path as string}>
                              <ItemIcon size={15} />
                              {label as string}
                            </Link>
                          </DropdownMenu.Item>
                        );
                      })}
                    {session.data.admin && (
                      <>
                        <DropdownMenu.Separator className="dropdown-separator" />
                        <DropdownMenu.Item onSelect={() => setNewBrand(true)}>
                          <Plus size={15} /> Brand
                        </DropdownMenu.Item>
                      </>
                    )}
                  </DropdownMenu.Content>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            </div>
          </header>
          <div className="brand-bar">
            <div className="brand-tabs" aria-label="Brands">
              {brands.data?.map((b) => (
                <button
                  key={b.id}
                  className={b.id === brandId ? "selected" : ""}
                  onClick={() => setSelected(b.id)}
                >
                  <span className="brand-tab-dot" />
                  {b.name}
                </button>
              ))}
              {session.data.admin && (
                <button
                  className="add-brand"
                  aria-label="Create brand"
                  title="Create brand"
                  onClick={() => setNewBrand(true)}
                >
                  <Plus size={15} />
                </button>
              )}
            </div>
          </div>
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
