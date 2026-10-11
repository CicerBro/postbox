import { describe, test, expect } from "bun:test";
import { resolve } from "node:path";
import { buildGuides, guideSources, legacyRoutes } from "../docs/content";

describe("embedded handbook", () => {
  const guides = buildGuides(resolve(import.meta.dirname, "../.."));
  test("six authored chapters keep references in the relevant workflow", () => {
    expect(guides.map((p) => p.id)).toEqual(["welcome", "getting-started", "sending-newsletters", "running-postbox", "moving-from-sendy", "development"]);
    expect(guideSources).not.toContain("README.md");
    expect(guideSources).not.toContain("MANUAL.md");
    expect(guides.find((p) => p.id === "getting-started")?.html).toContain("sns:Publish");
    expect(guides.find((p) => p.id === "running-postbox")?.html).toContain("POSTBOX_AMBIGUOUS_POLICY");
    expect(guides.find((p) => p.id === "moving-from-sendy")?.text).toContain("NO_BACKSLASH_ESCAPES");
    expect(guides.find((p) => p.id === "development")?.text).toContain("synchronous=NORMAL");
    for (const page of guides) {
      expect(page.html).not.toContain("<!-- reference:");
      expect(page.html).not.toContain("<h1");
      const ids = [...page.html.matchAll(/\bid="([^"]+)"/g)].map((match) => match[1]);
      expect(new Set(ids).size).toBe(ids.length);
      expect(page.toc.length).toBeLessThanOrEqual(8);
    }
  });
  test("chapter links, reference anchors and previous URLs resolve", () => {
    const assertTarget = (path: string, context: string) => {
      const [id, anchor] = path.replace(/^\/docs\/guides\/|^\/guides\//, "").split("#");
      const target = guides.find((p) => p.id === id);
      expect(target, `${context} links to missing ${id}`).toBeDefined();
      if (anchor) expect(target?.html, `${context} links to missing #${anchor}`).toContain(`id="${anchor}"`);
    };
    for (const page of guides) for (const match of page.html.matchAll(/href="(\/docs\/guides\/[^\"]+)"/g)) assertTarget(match[1], page.id);
    for (const [source, destination] of Object.entries(legacyRoutes)) assertTarget(destination, source);
    expect(guides[0].html).toContain('src="/docs/assets/postbox-campaigns.png"');
    expect(guides.find((p) => p.id === "running-postbox")?.html).toContain("https://github.com/CicerBro/postbox/blob/master/compose.yml");
  });
});
