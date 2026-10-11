import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Marked, type Tokens } from "marked";

export const slug = (text: string) => text.toLowerCase().replace(/[^a-z0-9\s-]/g, "").trim().replace(/\s+/g, "-");
const repo = "https://github.com/CicerBro/postbox/blob/master/";
const chapters = [
  { id: "welcome", file: "welcome" },
  { id: "getting-started", file: "getting-started" },
  { id: "aws", file: "aws" },
  { id: "sending-newsletters", file: "sending-newsletters" },
  { id: "running-postbox", file: "running-postbox" },
  { id: "configuration", file: "configuration" },
  { id: "moving-from-sendy", file: "moving-from-sendy" },
  { id: "development", file: "development" },
];
export const guideSources = chapters.map((p) => `docs/handbook/${p.file}.md`);

// Preserve links to the previous section-per-page documentation.
export const legacyRoutes: Record<string, string> = {
  "/guides/quick-start": "/guides/getting-started#start-an-instance",
  "/guides/first-run-and-configuration": "/guides/getting-started#start-an-instance",
  "/guides/environment-variables-and-cli-flags": "/guides/configuration",
  "/guides/environment-variables-and-cli-flags#shared-flags-and-matching-environment-variables": "/guides/configuration#startup-flags",
  "/guides/getting-started#aws-reference": "/guides/aws",
  "/guides/running-postbox#configuration-reference": "/guides/configuration",
  "/guides/running-postbox#configuration-startup-flags": "/guides/configuration#startup-flags",
  "/guides/moving-from-sendy#migration-reference": "/guides/moving-from-sendy#sendy-adapter-reference",
  "/guides/development#performance-reference": "/guides/development#measure-capacity",
  "/guides/aws-and-ses-events": "/guides/getting-started#connect-a-brand",
  "/guides/deployment": "/guides/running-postbox#deploy-the-service",
  "/guides/authentication-and-sessions": "/guides/running-postbox#manage-access",
  "/guides/reverse-proxies-and-rate-limits": "/guides/running-postbox#configure-https-and-proxy-access",
  "/guides/public-subscriptions-and-consent": "/guides/sending-newsletters#accept-new-signups",
  "/guides/delivery-and-recovery": "/guides/sending-newsletters#understand-delivery-outcomes",
  "/guides/maintenance-backup-and-restore": "/guides/running-postbox#back-up-and-restore",
  "/guides/sendy-migration": "/guides/moving-from-sendy",
  "/guides/migration-reference": "/guides/moving-from-sendy#sendy-adapter-reference",
  "/guides/campaign-workflows-and-csv": "/guides/sending-newsletters#export-data-and-receive-notifications",
  "/guides/architecture": "/guides/development#how-the-service-fits-together",
  "/guides/development-and-tests": "/guides/development#validate-a-change",
  "/guides/capacity-testing": "/guides/development#measure-capacity",
  "/guides/performance-profile": "/guides/development#measure-capacity",
};

export function buildGuides(root: string) {
  const sources = new Map(guideSources.map((source) => [source, readFileSync(resolve(root, source), "utf8")]));
  const destinations = new Map<string, string>();
  for (const chapter of chapters) {
    destinations.set(`docs/handbook/${chapter.file}.md`, `/docs/guides/${chapter.id}`);
  }
  const escape = (value: string) => value.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
  return chapters.map((chapter) => {
    const source = `docs/handbook/${chapter.file}.md`;
    const toc: { id: string; title: string }[] = [];
    const render = (text: string, sourceFile: string) => {
      const markdown = new Marked();
      const ids = new Map<string, number>();
      const rewrite = (href: string) => {
        if (/^(https?:|mailto:|\/)/i.test(href)) return href;
        const [file, anchor] = href.split("#");
        const target = file ? resolve(root, sourceFile, "..", file).slice(root.length + 1) : sourceFile;
        const linked = destinations.get(target);
        if (linked) return linked + (anchor ? `#${anchor}` : "");
        if (target.startsWith("docs/images/")) return `/docs/assets/${target.slice("docs/images/".length)}`;
        return repo + target + (anchor ? `#${anchor}` : "");
      };
      markdown.use({ renderer: {
        heading(token) {
          if (token.depth === 1) return "";
          const base = slug(token.text);
          const count = ids.get(base) ?? 0;
          ids.set(base, count + 1);
          const id = count ? `${base}-${count}` : base;
          if (token.depth === 2) toc.push({ id, title: token.text.replace(/`/g, "") });
          const depth = token.depth;
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
    const html = render(text, source);
    return { id: chapter.id, title, group: "Handbook", html, toc, text, source };
  });
}
