import { useEffect, useState, useRef, lazy, Suspense, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Routes,
  Route,
  NavLink,
  Navigate,
  Link,
  useNavigate,
  useLocation,
} from "react-router-dom";
import {
  Mail,
  Play,
  Pause,
  Search,
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
  ChevronsUpDown,
  BookOpen,
} from "lucide-react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { api, ApiError, setCsrfToken } from "./lib/api";
import { beginSession, expireSession, onSessionExpired, discardSessionDrafts } from "./lib/session-lifecycle";
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
  import("./pages/CampaignDetail").then((m) => ({ default: m.CampaignDetailPage })),
);
const CampaignReportPage = lazy(() =>
  import("./pages/CampaignReport").then((m) => ({
    default: m.CampaignReportPage,
  })),
);
const TemplatesPage = lazy(() =>
  import("./pages/Templates").then((m) => ({ default: m.TemplatesPage })),
);
const SubscribersPage = lazy(() =>
  import("./pages/Subscribers").then((m) => ({ default: m.SubscribersPage })),
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
  ["/subscribers", "Subscribers", Users],
  ["/templates", "Templates", FileCode],
  ["/segments", "Segments", Filter],
  ["/domains", "Domains", Globe],
  ["/api-keys", "API keys", KeyRound],
  ["/settings", "Settings", Settings],
] as const;
export default function App() {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [expired, setExpired] = useState(false);
  const identity = useRef<number | undefined>(undefined);
  useEffect(() => {
    const stop = onSessionExpired(() => {
      setCsrfToken("");
      setExpired(true);
      setNewBrand(false);
      setMobile(false);
      setSearchOpen(false);
      void client.cancelQueries();
      client.clear();
    });
    const changed = (event: StorageEvent) => {
      if (event.key === "postbox-auth-change") {
        expireSession();
        if (event.newValue?.startsWith("logout:")) discardSessionDrafts();
      }
    };
    window.addEventListener("storage", changed);
    return () => { stop(); window.removeEventListener("storage", changed); };
  }, [client]);
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
  const [searchOpen, setSearchOpen] = useState(false);
  const [pageSearch, setPageSearch] = useState("");
  const searchResults = navigation.filter(([, label]) =>
    label.toLowerCase().includes(pageSearch.trim().toLowerCase()),
  );
  useEffect(() => {
    function shortcut(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearchOpen((open) => !open);
      }
    }
    document.addEventListener("keydown", shortcut);
    return () => document.removeEventListener("keydown", shortcut);
  }, []);
  function goToPage(path: string) {
    navigate(path);
    setSearchOpen(false);
    setPageSearch("");
    setMobile(false);
  }
  const [theme, setTheme] = useThemePreference();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => {
      let s: Session;
      try { s = await api<Session>("/auth/session"); }
      catch (error) {
        if (identity.current !== undefined && error instanceof ApiError && error.status === 401) expireSession();
        throw error;
      }
      if (identity.current !== undefined && identity.current !== s.user_id) {
        expireSession();
        throw new Error("Your account changed. Sign in to continue.");
      }
      identity.current = s.user_id;
      beginSession(s.user_id);
      setCsrfToken(s.csrf_token);
      return s;
    },
    retry: false,
    enabled: !expired,
    staleTime: 30000,
    refetchInterval: expired ? false : 60000,
    refetchOnWindowFocus: true,
  });
  const brands = useQuery({
    queryKey: ["brands"],
    queryFn: ({ signal }) => api<Brand[]>("/brands?limit=500", { signal }),
    enabled: !expired && !!session.data,
  });
  const sending = useQuery({
    queryKey: ["sending"],
    queryFn: () => api<{ enabled: boolean }>("/sending"),
    enabled: !expired && !!session.data?.admin,
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
      expireSession();
      discardSessionDrafts();
      localStorage.setItem("postbox-auth-change", `logout:${crypto.randomUUID()}`);
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
  if (!expired && session.isPending)
    return (
      <main className="loading-screen" role="status">
        Loading Postbox…
      </main>
    );
  if (expired || !session.data) {
    if (
      !expired && session.error &&
      (!(session.error instanceof ApiError) || session.error.status !== 401)
    )
      return (
        <main className="loading-screen">
          <p role="alert">{session.error.message}</p>
          <Button onClick={() => session.refetch()}>Retry</Button>
        </main>
      );
    return (
      <Login
        expired={expired}
        onLogin={async () => {
          identity.current = undefined;
          const fresh = await api<Session>("/auth/session");
          beginSession(fresh.user_id);
          identity.current = fresh.user_id;
          setCsrfToken(fresh.csrf_token);
          client.removeQueries({ predicate: (query) => query.queryKey[0] !== "session" });
          client.setQueryData(["session"], fresh);
          setExpired(false);
          localStorage.setItem("postbox-auth-change", `login:${crypto.randomUUID()}`);
          const next = new URLSearchParams(window.location.search).get("next");
          if (next === "/docs" || next?.startsWith("/docs/")) window.location.assign(next);
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
          ? "A brand keeps your sender identity, subscribers and campaigns together."
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
  // Adapted from shadcn-admin's TeamSwitcher: one compact workspace switcher.
  const sidebarContent = (
    <>
      <div className="sidebar-header">
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button className="team-switcher" aria-label="Switch brand">
              <span className="brand-mark">
                <Mail size={16} />
              </span>
              <span className="sidebar-label team-copy">
                <strong>Postbox</strong>
                <small>{brand?.name ?? "Newsletter workspace"}</small>
              </span>
              <ChevronsUpDown size={16} className="sidebar-label" />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              className="dropdown-content brand-menu"
              side="right"
              align="start"
              sideOffset={4}
            >
              <DropdownMenu.Label className="dropdown-label">
                Brands
              </DropdownMenu.Label>
              {brands.data?.map((b) => (
                <DropdownMenu.Item
                  key={b.id}
                  onSelect={() => {
                    setSelected(b.id);
                    setMobile(false);
                  }}
                >
                  <span className="brand-menu-avatar">
                    {b.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="brand-menu-name">{b.name}</span>
                  {b.id === brandId && (
                    <span className="brand-selected">Selected</span>
                  )}
                </DropdownMenu.Item>
              ))}
              {session.data.admin && (
                <>
                  <DropdownMenu.Separator className="dropdown-separator" />
                  <DropdownMenu.Item
                    onSelect={() => {
                      setMobile(false);
                      setNewBrand(true);
                    }}
                  >
                    <span className="brand-menu-avatar">
                      <Plus size={16} />
                    </span>{" "}
                    Add brand
                  </DropdownMenu.Item>
                </>
              )}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
      <nav aria-label="Main navigation">
        {[
          ["General", navigation.slice(0, 5)],
          ["Other", navigation.slice(5)],
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
                    `nav-item ${isActive || (path === "/" && location.pathname === "/overview") ? "active" : ""}`
                  }
                >
                  <Icon size={16} />
                  <span className="sidebar-label">{label}</span>
                </NavLink>
              ),
            )}
          </div>
        ))}
      </nav>
      <a className="nav-item" href="/docs" title="Documentation" target="_blank" rel="noreferrer">
        <BookOpen size={16} />
        <span className="sidebar-label">Documentation</span>
      </a>
      <div className="sidebar-bottom">
        {session.data.admin ? (
          <>
            <button
              className={`sending-pill ${sending.data?.enabled ? "sending-on" : ""}`}
              disabled={changeSending.isPending || !sending.data}
              onClick={() => changeSending.mutate(!sending.data?.enabled)}
              aria-label={
                !sending.data
                  ? "Sending status unavailable"
                  : sending.data.enabled
                    ? "Sending live. Click to pause all sending"
                    : "Sending paused. Click to resume all sending"
              }
              title={
                !sending.data
                  ? "Sending status unavailable"
                  : sending.data.enabled
                    ? "Sending live. Click to pause all sending"
                    : "Sending paused. Click to resume all sending"
              }
            >
              <span className={`sending-status-dot ${!sending.data ? "sending-status-unknown" : ""}`} aria-hidden="true" />
              <span className="sidebar-label">
                {sending.data
                  ? sending.data.enabled
                    ? "Sending live"
                    : "Sending paused"
                  : "Status unavailable"}
              </span>
              {sending.data?.enabled ? (
                <Play className="sidebar-label sending-action-icon" size={14} aria-hidden="true" />
              ) : (
                <Pause className="sidebar-label sending-action-icon" size={14} aria-hidden="true" />
              )}
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
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button className="account-trigger" aria-label="Account menu">
              <span className="avatar">{session.data.admin ? "AD" : "US"}</span>
              <span className="sidebar-label account-copy">
                <strong>
                  {session.data.admin ? "Administrator" : "Team member"}
                </strong>
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
        <Dialog open={searchOpen} onOpenChange={setSearchOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Find a page</DialogTitle>
              <DialogDescription>
                Search workspace navigation. Select a page to open it.
              </DialogDescription>
            </DialogHeader>
            <Input
              aria-label="Search pages"
              placeholder="Type a page name…"
              value={pageSearch}
              onChange={(event) => setPageSearch(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && searchResults[0]) {
                  event.preventDefault();
                  goToPage(searchResults[0][0]);
                }
              }}
            />
            <div className="page-search-results">
              {searchResults.map(([path, label, Icon]) => (
                <Button
                  key={path}
                  variant="ghost"
                  className="page-search-result"
                  onClick={() => goToPage(path)}
                >
                  <Icon size={16} />
                  {label}
                </Button>
              ))}
              {searchResults.length === 0 && (
                <p className="muted" role="status">
                  No matching pages.
                </p>
              )}
            </div>
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
              <nav
                className="header-navigation"
                aria-label="Workspace navigation"
              >
                {[
                  ["/", "Overview"],
                  ["/campaigns", "Campaigns"],
                  ["/subscribers", "Subscribers"],
                  ["/settings", "Settings"],
                ].map(([path, label]) => (
                  <NavLink
                    key={path}
                    to={path}
                    end={path === "/"}
                    className={({ isActive }) =>
                      isActive ||
                      (path === "/" && location.pathname === "/overview")
                        ? "active"
                        : ""
                    }
                  >
                    {label}
                  </NavLink>
                ))}
              </nav>
            </div>
            <div className="topbar-actions">
              <Button
                variant="outline"
                className="page-search"
                onClick={() => {
                  setPageSearch("");
                  setSearchOpen(true);
                }}
                aria-label="Find a page"
              >
                <Search size={16} />
                <span className="page-search-label">Search</span>
                <kbd>⌘ K</kbd>
              </Button>
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
                        ["/subscribers?new=list", "List", Users],
                        ["/subscribers?new=subscriber", "Subscriber", Users],
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
                <Routes key={`${session.data.user_id}:${brandId}`}>
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
                        element={
                          <Navigate
                            to={`/subscribers${location.search}${location.hash}`}
                            replace
                          />
                        }
                      />
                      <Route
                        path="/subscribers"
                        element={<SubscribersPage brandId={brandId} />}
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
