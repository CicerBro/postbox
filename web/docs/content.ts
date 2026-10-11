import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Marked, type Tokens } from "marked";

export const slug = (text: string) => text.toLowerCase().replace(/[^a-z0-9\s-]/g, "").trim().replace(/\s+/g, "-");
const repo = "https://github.com/CicerBro/postbox/blob/master/";
const chapters = [
  { id: "welcome", file: "welcome" },
  { id: "getting-started", file: "getting-started", reference: "aws" },
  { id: "sending-newsletters", file: "sending-newsletters" },
  { id: "running-postbox", file: "running-postbox", reference: "configuration" },
  { id: "moving-from-sendy", file: "moving-from-sendy", reference: "migration" },
  { id: "development", file: "development", reference: "performance" },
];
const references: Record<string, { source: string; title: string; id: string }> = {
  aws: { source: "docs/reference/aws.md", title: "AWS permissions and event resources", id: "aws-reference" },
  configuration: { source: "docs/reference/configuration.md", title: "Configuration reference", id: "configuration-reference" },
  migration: { source: "docs/migration.md", title: "Sendy adapter reference", id: "migration-reference" },
  performance: { source: "docs/performance-profile.md", title: "Performance measurements and evidence", id: "performance-reference" },
};
export const guideSources = [...chapters.map((p) => `docs/handbook/${p.file}.md`), ...Object.values(references).map((r) => r.source)];

// Preserve links to the previous section-per-page documentation.
export const legacyRoutes: Record<string, string> = {
  "/guides/quick-start": "/guides/getting-started#start-an-instance",
  "/guides/first-run-and-configuration": "/guides/getting-started#start-an-instance",
  "/guides/environment-variables-and-cli-flags": "/guides/running-postbox#configuration-reference",
  "/guides/environment-variables-and-cli-flags#shared-flags-and-matching-environment-variables": "/guides/running-postbox#configuration-startup-flags",
  "/guides/aws-and-ses-events": "/guides/getting-started#connect-a-brand",
  "/guides/deployment": "/guides/running-postbox#deploy-the-service",
  "/guides/authentication-and-sessions": "/guides/running-postbox#manage-access",
  "/guides/reverse-proxies-and-rate-limits": "/guides/running-postbox#configure-https-and-proxy-access",
  "/guides/public-subscriptions-and-consent": "/guides/sending-newsletters#accept-new-signups",
  "/guides/delivery-and-recovery": "/guides/sending-newsletters#understand-delivery-outcomes",
  "/guides/maintenance-backup-and-restore": "/guides/running-postbox#back-up-and-restore",
  "/guides/sendy-migration": "/guides/moving-from-sendy",
  "/guides/migration-reference": "/guides/moving-from-sendy#migration-reference",
  "/guides/campaign-workflows-and-csv": "/guides/sending-newsletters#export-data-and-receive-notifications",
  "/guides/architecture": "/guides/development#how-the-service-fits-together",
  "/guides/development-and-tests": "/guides/development#validate-a-change",
  "/guides/capacity-testing": "/guides/development#measure-capacity",
  "/guides/performance-profile": "/guides/development#performance-reference",
};

export function buildGuides(root: string) {
  const sources = new Map(guideSources.map((source) => [source, readFileSync(resolve(root, source), "utf8")]));
  const destinations = new Map<string, { path: string; prefix: string; entry?: string }>();
  for (const chapter of chapters) {
    destinations.set(`docs/handbook/${chapter.file}.md`, { path: `/docs/guides/${chapter.id}`, prefix: "" });
    if (chapter.reference) destinations.set(references[chapter.reference].source, { path: `/docs/guides/${chapter.id}`, prefix: chapter.reference + "-", entry: references[chapter.reference].id });
  }
  const escape = (value: string) => value.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
  return chapters.map((chapter) => {
    const source = `docs/handbook/${chapter.file}.md`;
    const toc: { id: string; title: string }[] = [];
    const render = (text: string, sourceFile: string, prefix = "", inReference = false) => {
      const markdown = new Marked();
      const ids = new Map<string, number>();
      const rewrite = (href: string) => {
        if (/^(https?:|mailto:|\/)/i.test(href)) return href;
        const [file, anchor] = href.split("#");
        const target = file ? resolve(root, sourceFile, "..", file).slice(root.length + 1) : sourceFile;
        const linked = destinations.get(target);
        if (linked) return linked.path + (anchor ? `#${linked.prefix}${anchor}` : linked.entry ? `#${linked.entry}` : "");
        if (target.startsWith("docs/images/")) return `/docs/assets/${target.slice("docs/images/".length)}`;
        return repo + target + (anchor ? `#${anchor}` : "");
      };
      markdown.use({ renderer: {
        heading(token) {
          if (token.depth === 1) return "";
          const base = prefix + slug(token.text);
          const count = ids.get(base) ?? 0;
          ids.set(base, count + 1);
          const id = count ? `${base}-${count}` : base;
          if (!inReference && token.depth === 2) toc.push({ id, title: token.text.replace(/`/g, "") });
          const depth = Math.min(6, token.depth + (inReference ? 1 : 0));
          return `<h${depth} id="${id}">${this.parser.parseInline(token.tokens)}</h${depth}>`;
        },
        link(token) { return `<a href="${escape(rewrite(token.href))}">${this.parser.parseInline(token.tokens)}</a>`; },
        image(token) { return `<img src="${escape(rewrite(token.href))}" alt="${escape(token.text)}" loading="lazy" />`; },
      } });
      return markdown.parse(text) as string;
    };
    const text = sources.get(source)!;
    const tokens = new Marked().lexer(text);
    const title = (tokens.find((t) => t.type === "heading" && t.depth === 1) as Tokens.Heading).text;
    let html = render(text.replace(/<!-- reference:\w+ -->/g, ""), source);
    let searchText = text;
    if (chapter.reference) {
      const ref = references[chapter.reference];
      const referenceText = sources.get(ref.source)!;
      toc.push({ id: ref.id, title: ref.title });
      html += `<details class="docs-reference-details" id="${ref.id}"><summary>${ref.title}<span>Full reference</span></summary><div>${render(referenceText, ref.source, chapter.reference + "-", true)}</div></details>`;
      searchText += "\n" + referenceText;
    }
    return { id: chapter.id, title, group: "Handbook", html, toc, text: searchText, source };
  });
}
