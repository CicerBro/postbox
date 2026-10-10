import { describe, expect, test } from "bun:test";
import { sandboxVisualFrames } from "../src/components/email-source";

describe("visual canvas lifecycle safety", () => {
  test("sandboxes visible and hidden helper frames and removes late frames after disposal", () => {
    const callbacks: ((frame: { el: HTMLIFrameElement }) => void)[] = [];
    const model = () => ({
      on(event: string, callback: (frame: { el: HTMLIFrameElement }) => void) {
        expect(event).toBe("frame:render");
        callbacks.push(callback);
      },
    });
    let disposed = false;
    sandboxVisualFrames([model(), model()], () => disposed);
    expect(callbacks).toHaveLength(2);
    for (const callback of callbacks) {
      const attributes: Record<string, string> = {};
      let removed = false;
      const el = {
        setAttribute(name: string, value: string) {
          attributes[name] = value;
        },
        remove() {
          removed = true;
        },
      } as unknown as HTMLIFrameElement;
      callback({ el });
      expect(attributes.sandbox).toBe("allow-same-origin");
      expect(removed).toBe(false);
      disposed = true;
      callback({ el });
      expect(removed).toBe(true);
      disposed = false;
    }
  });
});
import {
  buildVisualCandidate,
  safeVisualAttributes,
} from "../src/components/email-source";
describe("proposed visual full-document export", () => {
  test("replaces editable styles while retaining metadata, Outlook comments and document envelope", () => {
    const conditional =
      "<!--[if mso]><style>.column{width:300px}</style><![endif]-->";
    const source =
      '<!doctype HTML><html lang="nl"><head><meta charset="utf-8"><style>.old{color:red}</style>' +
      conditional +
      "</head><!-- before body --><body><p>Original</p></body><!-- after body --></html>";
    const candidate = buildVisualCandidate(
      source,
      '<body class="newsletter"><p>Edited</p></body>',
      ".newsletter{color:#111}",
    );
    expect(candidate).toStartWith(
      '<!doctype HTML><html lang="nl"><head><meta charset="utf-8">',
    );
    expect(candidate).toContain(conditional);
    expect(candidate).toContain("<!-- before body -->");
    expect(candidate).toContain("<!-- after body -->");
    expect(candidate).not.toContain(".old{color:red}");
    expect(candidate).toContain(".newsletter{color:#111}");
    expect(source).toContain("Original");
    expect(source).toContain(".old{color:red}");
  });
  test("deleting visual CSS removes ordinary styles but keeps conditional styles", () => {
    const source =
      "<html><head><style>.deleted{color:red}</style><!--[if mso]><style>.outlook{color:blue}</style><![endif]--><style>.also-deleted{color:red}</style></head><body></body></html>";
    const candidate = buildVisualCandidate(source, "<body></body>", "");
    expect(candidate).not.toContain(".deleted");
    expect(candidate).not.toContain(".also-deleted");
    expect(candidate).toContain(".outlook{color:blue}");
    expect(candidate).not.toContain("data-postbox-visual");
  });
  test("repeated apply cycles replace the generated style block", () => {
    const source =
      "<html><head><style>.first{color:red}</style><style>.second{color:blue}</style></head><body></body></html>";
    const first = buildVisualCandidate(
      source,
      "<body><p>One</p></body>",
      ".one{color:red}",
    );
    const second = buildVisualCandidate(
      first,
      "<body><p>Two</p></body>",
      ".two{color:blue}",
    );
    expect(second.match(/<style\b/g)).toHaveLength(1);
    expect(second).not.toContain(".one");
    expect(second).toContain(".two");
    expect(
      buildVisualCandidate(
        second,
        "<body><p>Two</p></body>",
        ".two{color:blue}",
      ),
    ).toBe(second);
  });
  test("exports the visual body wrapper exactly once", () => {
    const candidate = buildVisualCandidate(
      '<!DOCTYPE html><html><head></head><body id="old"></body></html>',
      '<body id="new"><p>Content</p></body>',
      "#new{margin:0}",
    );
    expect(candidate.match(/<body\b/g)).toHaveLength(1);
    expect(candidate).toContain('<body id="new"><p>Content</p></body>');
  });
  test("supplies document structure for an HTML fragment", () => {
    expect(
      buildVisualCandidate("<p>Hello</p>", "<body><p>Hello</p></body>", ""),
    ).toStartWith("<!DOCTYPE html>\n<html>\n<head>");
  });
});
describe("visual import attribute safety", () => {
  test("removes body handlers and dangerous case or whitespace variants", () => {
    expect(
      safeVisualAttributes([
        ["onclick", "parent.document.body.remove()"],
        ["onMouseOver", "alert(1)"],
        ["background", " \tJaVa\nScRiPt:alert(1)"],
        ["href", "VBSCRIPT:msgbox(1)"],
        ["srcdoc", "<script>parent.alert(1)</script>"],
        ["src", "data:text/html,<script>alert(1)</script>"],
        ["srcset", 'data:image/svg+xml,<svg onload="alert(1)">'],
        ["style", "width:expression(alert(1))"],
      ]),
    ).toEqual({});
  });
  test("retains legitimate email wrapper attributes and safe links", () => {
    const attributes = [
      ["bgcolor", "#ffffff"],
      ["class", "newsletter"],
      ["id", "email"],
      ["style", "margin:0;color:#111"],
      ["href", "mailto:hello@example.com"],
      ["src", "cid:logo"],
      ["background", "https://example.com/bg.png"],
    ] as const;
    expect(safeVisualAttributes(attributes)).toEqual(
      Object.fromEntries(attributes),
    );
  });
});
