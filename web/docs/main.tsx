import { useEffect, useMemo, useState, lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Link, NavLink, Navigate, useLocation } from "react-router-dom";
import { BookOpen, ArrowLeft, ArrowRight, Search, Menu, Sun, Moon, Code2 } from "lucide-react";
import guides, { legacyRoutes } from "virtual:postbox-guides";
import { useThemePreference } from "../src/lib/theme";
import "./style.css";

const APIReference = lazy(() => import("./reference"));

function Docs() {
  const location = useLocation();
  const [query, setQuery] = useState("");
  const [menu, setMenu] = useState(false);
  const [theme, setTheme] = useThemePreference();
  const [session, setSession] = useState<{ user_id: number; csrf_token: string }>();
  const [error, setError] = useState("");
  const api = location.pathname === "/api";
  const redirect = legacyRoutes[location.pathname + location.hash] ?? legacyRoutes[location.pathname];
  const page = location.pathname === "/" ? guides[0] : guides.find((p) => location.pathname === `/guides/${p.id}`);
  const index = page ? guides.indexOf(page) : -1;
  const matches = useMemo(() => guides.filter((p) => `${p.title} ${p.text}`.toLowerCase().includes(query.toLowerCase().trim())), [query]);
  const groups = [...new Set(guides.map((p) => p.group))];
  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const check = async () => {
      try {
        const response = await fetch("/api/v1/auth/session", { credentials: "same-origin", cache: "no-store", signal: controller.signal });
        if (response.status === 401 || response.status === 403) { window.location.replace("/login?next=" + encodeURIComponent(window.location.pathname)); return; }
        if (!response.ok) throw new Error("Could not check your session. Reload to try again.");
        const current = await response.json();
        if (active) {
          setSession((previous) => {
            if (previous && previous.user_id !== current.user_id) window.location.reload();
            return current;
          });
          setError("");
        }
      } catch (e) { if (active && !controller.signal.aborted) setError((e as Error).message); }
    };
    void check();
    const timer = window.setInterval(check, 60000);
    const changed = (event: StorageEvent) => { if (event.key === "postbox-auth-change") window.location.reload(); };
    window.addEventListener("storage", changed);
    window.addEventListener("focus", check);
    return () => { active = false; controller.abort(); clearInterval(timer); window.removeEventListener("storage", changed); window.removeEventListener("focus", check); };
  }, []);
  useEffect(() => {
    setMenu(false);
    document.title = `${api ? "API reference" : page?.title ?? "Page not found"} · Postbox docs`;
    if (location.hash) requestAnimationFrame(() => {
      const target = document.getElementById(decodeURIComponent(location.hash.slice(1)));
      const details = target?.closest("details");
      if (details) details.open = true;
      target?.scrollIntoView();
    });
    else window.scrollTo(0, 0);
  }, [location.pathname, location.hash, api, page, !!session]);

  if (error || !session) return <main className="docs-status" role={error ? "alert" : "status"}>{error || "Loading documentation…"}{error && <a href="">Reload</a>}</main>;
  if (redirect) return <Navigate to={redirect} replace />;
  return <>
    <a className="skip-link" href="#docs-main">Skip to content</a>
    <header className="docs-header">
      <Link to="/" className="docs-brand"><span><BookOpen size={20} /></span><strong>Postbox <small>Docs</small></strong></Link>
      <nav aria-label="Documentation sections">
        <NavLink to="/" className={() => !api ? "selected" : ""}><BookOpen size={16} /> Guides</NavLink>
        <NavLink to="/api"><Code2 size={16} /> API reference</NavLink>
      </nav>
      <div className="docs-header-actions">
        <button aria-label="Toggle color theme" onClick={() => setTheme(document.documentElement.classList.contains("dark") ? "light" : "dark")}>{theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}</button>
        <a href="/" className="docs-back" aria-label="Back to app"><ArrowLeft size={16} /><span>Back to app</span></a>
        {!api && <button className="docs-menu-button" aria-label="Toggle guide navigation" aria-expanded={menu} onClick={() => setMenu(!menu)}><Menu size={20} /></button>}
      </div>
    </header>
    {api ? <main id="docs-main" className="docs-reference"><Suspense fallback={<p className="docs-status">Loading API reference…</p>}><APIReference csrf={session.csrf_token} /></Suspense></main> :
      <div className="docs-layout">
        <aside className={`docs-sidebar ${menu ? "open" : ""}`}>
          <label className="docs-search"><Search size={17} /><input type="search" aria-label="Search documentation" placeholder="Search guides…" value={query} onChange={(e) => setQuery(e.target.value)} /></label>
          <nav aria-label="Guides">{groups.map((group) => {
            const items = matches.filter((p) => p.group === group);
            return items.length > 0 && <section key={group}><h2>{group}</h2>{items.map((p) => <NavLink key={p.id} to={`/guides/${p.id}`} className={() => page?.id === p.id ? "selected" : ""}>{p.title}</NavLink>)}</section>;
          })}{matches.length === 0 && <p role="status">No matching guides.</p>}</nav>
        </aside>
        <main id="docs-main" className="docs-article">
          {page ? <>
            <p className="docs-eyebrow">{page.group}</p><h1>{page.title}</h1>
            <article className="docs-prose" dangerouslySetInnerHTML={{ __html: page.html }} />
            <nav className="docs-pagination" aria-label="Adjacent guides">
              {index > 0 ? <Link to={`/guides/${guides[index - 1].id}`}><span><ArrowLeft size={16} /> Previous</span>{guides[index - 1].title}</Link> : <span />}
              {index < guides.length - 1 && <Link to={`/guides/${guides[index + 1].id}`}><span>Next <ArrowRight size={16} /></span>{guides[index + 1].title}</Link>}
            </nav>
          </> : <><h1>Page not found</h1><p>This guide does not exist.</p><Link to="/">Browse the documentation</Link></>}
        </main>
        {page && <aside className="docs-toc" aria-label="On this page"><h2>On this page</h2><a href="#docs-main">{page.title}</a>{page.toc.map((h) => <a key={h.id} href={`#${h.id}`}>{h.title}</a>)}<Link to="/api" className="docs-toc-api">Explore the API <ArrowRight size={14} /></Link></aside>}
      </div>}
  </>;
}

createRoot(document.getElementById("root")!).render(<BrowserRouter basename="/docs"><Docs /></BrowserRouter>);
