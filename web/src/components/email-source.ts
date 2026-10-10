type VisualFrameModel = {
  on(
    event: string,
    listener: (frame: { el: HTMLIFrameElement }) => void,
  ): unknown;
};

/** Protect both the visible canvas and GrapesJS's hidden shallow-model canvas. */
export function sandboxVisualFrames(
  models: VisualFrameModel[],
  isDisposed: () => boolean,
) {
  const protect = (frame: { el: HTMLIFrameElement }) => {
    frame.el.setAttribute("sandbox", "allow-same-origin");
    if (isDisposed()) frame.el.remove();
  };
  for (const model of models) model.on("frame:render", protect);
}

/** Filter all visual import attributes, including the body wrapper which bypasses GrapesJS parsing. */
export function safeVisualAttributes(
  attributes: Iterable<readonly [string, string]>,
): Record<string, string> {
  const safe: Record<string, string> = {};
  for (const [name, value] of attributes) {
    const key = name.toLowerCase();
    const normalized = value
      .replace(/[\u0000-\u0020\u007f]/g, "")
      .toLowerCase();
    if (key.startsWith("on") || ["srcdoc", "srcset", "ping"].includes(key))
      continue;
    if (/^(javascript|vbscript):/.test(normalized)) continue;
    if (normalized.startsWith("data:") && !/^(src|background)$/.test(key))
      continue;
    if (
      normalized.startsWith("data:") &&
      !/^data:image\/(png|gif|jpe?g|webp);/.test(normalized)
    )
      continue;
    if (
      key === "style" &&
      /expression\s*\(|(?:javascript|vbscript)\s*:|-moz-binding|behavior\s*:/i.test(
        value,
      )
    )
      continue;
    safe[name] = value;
  }
  return safe;
}

/** Work on a detached parsed copy only. Never modify the raw source. */
export function sanitizeVisualDocument(document: Document): void {
  for (const element of Array.from(
    document.querySelectorAll(
      "script,iframe,frame,frameset,object,embed,applet,base,meta,link,svg,math",
    ),
  ))
    element.remove();
  for (const element of [
    document.body,
    ...Array.from(document.body.querySelectorAll("*")),
  ]) {
    const attributes = Array.from(element.attributes).map(
      (attribute) => [attribute.name, attribute.value] as const,
    );
    const safe = safeVisualAttributes(attributes);
    for (const [name] of attributes)
      if (!(name in safe)) element.removeAttribute(name);
  }
}

/** Skip comments so Outlook-only styles stay untouched and outside the visual CSS model. */
function replaceHeadStyles(head: string, css: string): string {
  let inserted = false;
  const style = css.trim()
    ? `<style data-postbox-visual>\n${css}\n</style>`
    : "";
  const result = head.replace(
    /<!--[\s\S]*?-->|<style\b[^>]*>[\s\S]*?<\/style\s*>/gi,
    (token) => {
      if (token.startsWith("<!--")) return token;
      if (inserted) return "";
      inserted = true;
      return style;
    },
  );
  return inserted || !style
    ? result
    : result.replace(/<\/head\s*>/i, `${style}\n</head>`);
}

/** Build only a proposed visual export. Callers must show a diff before applying it. */
export function buildVisualCandidate(
  original: string,
  body: string,
  css: string,
): string {
  const originalHead = original.match(/<head\b[^>]*>[\s\S]*?<\/head\s*>/i)?.[0];
  const originalBody = original.match(/<body\b[^>]*>[\s\S]*?<\/body\s*>/i)?.[0];
  if (originalHead && originalBody) {
    return original
      .replace(originalHead, () => replaceHeadStyles(originalHead, css))
      .replace(originalBody, () => body);
  }
  const doctype = original.match(/<!doctype[^>]*>/i)?.[0] ?? "<!DOCTYPE html>";
  const documentTag = original.match(/<html\b[^>]*>/i)?.[0] ?? "<html>";
  return `${doctype}\n${documentTag}\n${replaceHeadStyles(originalHead ?? "<head></head>", css)}\n${body}\n</html>`;
}
