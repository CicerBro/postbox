import { useEffect, useRef, useState } from "react";
import { EditorState } from "@codemirror/state";
import { EditorView, keymap, lineNumbers } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { html } from "@codemirror/lang-html";
import { MergeView } from "@codemirror/merge";
import type { Editor } from "grapesjs";
import "grapesjs/dist/css/grapes.min.css";
import { useQuery } from "@tanstack/react-query";
import {
  buildVisualCandidate,
  sanitizeVisualDocument,
  sandboxVisualFrames,
} from "./email-source";
import { api } from "../lib/api";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { SelectField } from "./ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "./ui/dialog";
import { ErrorMessage } from "../pages/campaign-shared";

type Props = {
  value: string;
  onChange: (html: string) => void;
  brandId: number;
  readOnly?: boolean;
  renderPreview?: (
    subscriberId: number,
  ) => Promise<{ subject: string; html: string; text: string }>;
  dirty?: boolean;
};
// Mount this inside the portal. The parent can run its effects before Radix
// mounts dialog contents, so a parent-owned ref can miss the opening entirely.
function HtmlChangeReview({
  original,
  proposed,
}: {
  original: string;
  proposed: string;
}) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!host.current) return;
    const extensions = (label: string) => [
      lineNumbers(),
      html(),
      EditorView.contentAttributes.of({ "aria-label": label }),
      EditorState.readOnly.of(true),
      EditorView.editable.of(false),
      EditorView.lineWrapping,
    ];
    const diff = new MergeView({
      parent: host.current,
      a: { doc: original, extensions: extensions("Original HTML source") },
      b: { doc: proposed, extensions: extensions("Proposed HTML source") },
      highlightChanges: true,
      gutter: true,
    });
    diff.dom.style.height = "100%";
    return () => diff.destroy();
  }, [original, proposed]);
  return (
    <div
      className="overflow-x-auto rounded-md border"
      role="region"
      aria-label="HTML changes comparison"
      tabIndex={0}
    >
      <div style={{ minWidth: 640 }}>
        <div className="grid grid-cols-2 border-b bg-muted px-3 py-2 text-sm font-semibold">
          <span>Original HTML</span>
          <span>Proposed HTML</span>
        </div>
        <div
          ref={host}
          style={{ height: "min(52vh, 540px)", minHeight: 180 }}
        />
      </div>
    </div>
  );
}
export function EmailEditor({
  value,
  onChange,
  brandId,
  readOnly = false,
  renderPreview,
  dirty = false,
}: Props) {
  const rawHost = useRef<HTMLDivElement>(null),
    visualHost = useRef<HTMLDivElement>(null);
  const cm = useRef<EditorView | null>(null),
    visual = useRef<Editor | null>(null),
    change = useRef(onChange);
  change.current = onChange;
  const [mode, setMode] = useState<"raw" | "visual">("raw"),
    [mobile, setMobile] = useState(false),
    [candidate, setCandidate] = useState<string | null>(null),
    [original, setOriginal] = useState("");
  const [tag, setTag] = useState("name"),
    [fallback, setFallback] = useState(""),
    [subscriber, setSubscriber] = useState(""),
    [preview, setPreview] = useState<{
      subject: string;
      html: string;
      text: string;
    } | null>(null),
    [error, setError] = useState<unknown>(null),
    [loading, setLoading] = useState(false);
  const fields = useQuery({
    queryKey: ["editor-fields", brandId],
    queryFn: () =>
      api<{ id: number; key: string; label: string }[]>("/custom-fields", {
        brandId,
      }),
    enabled: brandId > 0,
  });
  useEffect(() => {
    if (!rawHost.current || mode !== "raw") return;
    const editor = new EditorView({
      parent: rawHost.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          lineNumbers(),
          history(),
          keymap.of([...defaultKeymap, ...historyKeymap]),
          html(),
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ "aria-label": "Full HTML source" }),
          EditorState.readOnly.of(readOnly),
          EditorView.editable.of(!readOnly),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) change.current(u.state.doc.toString());
          }),
          EditorView.theme({
            "&": { height: "480px" },
            ".cm-scroller": { overflow: "auto" },
          }),
        ],
      }),
    });
    cm.current = editor;
    return () => {
      editor.destroy();
      cm.current = null;
    };
  }, [mode, readOnly]);
  useEffect(() => {
    const editor = cm.current;
    if (editor && editor.state.doc.toString() !== value)
      editor.dispatch({
        changes: { from: 0, to: editor.state.doc.length, insert: value },
      });
    setPreview(null);
  }, [value]);
  useEffect(() => {
    if (mode !== "visual" || !visualHost.current) return;
    let disposed = false,
      instance: Editor | null = null;
    // Each lifecycle gets its own container, so a late GrapesJS frame callback
    // cannot attach an orphan frame to a subsequent editor's live host.
    const container = document.createElement("div");
    visualHost.current.appendChild(container);
    setLoading(true);
    Promise.all([import("grapesjs"), import("grapesjs-preset-newsletter")])
      .then(([g, p]) => {
        if (disposed) return;
        const editor = g.default.init({
          container,
          autorender: false,
          height: "600px",
          storageManager: false,
          cssIcons: "",
          assetManager: { upload: false },
          plugins: [
            (editor) =>
              p.default(editor, {
                inlineCss: false,
                useCustomTheme: false,
                blocks: [
                  "sect100",
                  "sect50",
                  "sect30",
                  "sect37",
                  "button",
                  "divider",
                  "text",
                  "text-sect",
                ],
              }),
          ],
          parser: {
            optionsHtml: {
              allowScripts: false,
              allowUnsafeAttr: false,
              allowUnsafeAttrValue: false,
            },
          },
          canvas: { scripts: [], styles: [] },
        });
        instance = editor;
        // EditorView.render also mounts a hidden shallow canvas used for style
        // processing. Its events belong to the shallow model, not the editor.
        sandboxVisualFrames(
          [editor, editor.getModel().shallow],
          () => disposed,
        );
        if (disposed) {
          editor.destroy();
          return;
        }
        editor.render();
        const document = new DOMParser().parseFromString(original, "text/html");
        sanitizeVisualDocument(document);
        editor.setStyle(
          Array.from(document.head.querySelectorAll("style"))
            .map((s) =>
              s.media
                ? `@media ${s.media}{${s.textContent ?? ""}}`
                : (s.textContent ?? ""),
            )
            .join("\n"),
        );
        editor.setComponents(document.body.innerHTML);
        editor
          .getWrapper()
          ?.addAttributes(
            Object.fromEntries(
              Array.from(document.body.attributes).map((a) => [
                a.name,
                a.value,
              ]),
            ),
          );
        visual.current = editor;
      })
      .catch((e) => {
        if (!disposed) setError(e);
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
      if (visual.current === instance) visual.current = null;
      instance?.destroy();
      container.remove();
    };
  }, [mode, original]);
  function insertTag() {
    const text = `{{ ${tag}${fallback ? ` | ${JSON.stringify(fallback)}` : ""} }}`;
    const view = cm.current;
    if (view) {
      const s = view.state.selection.main;
      view.dispatch({
        changes: { from: s.from, to: s.to, insert: text },
        selection: { anchor: s.from + text.length },
      });
      view.focus();
    }
  }
  function propose() {
    if (!visual.current) return;
    setCandidate(
      buildVisualCandidate(
        original,
        visual.current.getHtml({ asDocument: false }),
        visual.current.getCss() ?? "",
      ),
    );
  }
  async function rendered() {
    setLoading(true);
    setError(null);
    try {
      const id = Number(subscriber);
      if (!Number.isSafeInteger(id) || id <= 0)
        throw new Error("Enter a valid subscriber ID");
      if (dirty)
        throw new Error(
          "Save the current draft before rendering with subscriber data.",
        );
      if (renderPreview) setPreview(await renderPreview(id));
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant={mode === "raw" ? "default" : "outline"}
          onClick={() => setMode("raw")}
        >
          HTML source
        </Button>
        {!readOnly && (
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setOriginal(value);
              setMode("visual");
              setError(null);
            }}
            disabled={mode === "visual"}
          >
            Visual editor
          </Button>
        )}
        <Button
          type="button"
          variant="outline"
          onClick={() => setMobile(!mobile)}
        >
          {mobile ? "Desktop preview" : "Mobile preview"}
        </Button>
      </div>
      <ErrorMessage error={error ?? fields.error} />
      {mode === "raw" ? (
        <>
          <div className="flex flex-wrap items-end gap-2">
            {!readOnly && (
              <>
                <label htmlFor="editor-merge-tag" className="block space-y-2 text-sm">
                  Merge tag
                  <SelectField
                    id="editor-merge-tag"
                    value={tag}
                    onValueChange={setTag}
                    options={[
                      ...[
                        "name",
                        "email",
                        "unsubscribe",
                        "webversion",
                        "currentdaydate",
                        "currentmonth",
                        "currentyear",
                      ].map((value) => ({ value, label: value })),
                      ...(fields.data?.map((f) => ({
                        value: f.key,
                        label: `${f.label} (${f.key})`,
                      })) ?? []),
                    ]}
                  />
                </label>
                <label className="block space-y-2 text-sm">
                  Fallback
                  <Input
                    value={fallback}
                    onChange={(e) => setFallback(e.target.value)}
                    placeholder="Optional"
                  />
                </label>
                <Button type="button" variant="outline" onClick={insertTag}>
                  Insert tag
                </Button>
              </>
            )}
          </div>
          <div className="grid gap-4 xl:grid-cols-2">
            <div
              ref={rawHost}
              className="min-w-0 overflow-hidden rounded-md border"
              aria-label="Full HTML source"
            />
            <div className="overflow-auto rounded-md border bg-white p-2">
              <iframe
                title="Email preview"
                sandbox=""
                referrerPolicy="no-referrer"
                srcDoc={preview?.html ?? value}
                className="mx-auto h-[480px] border-0"
                style={{ width: mobile ? 375 : "100%", maxWidth: "100%" }}
              />
            </div>
          </div>
        </>
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Visual editing can normalize table markup, styles and Outlook
            conditional comments. Your saved source stays intact until you
            review and apply the proposed full-document diff.
          </p>
          {loading && <p role="status">Loading visual editor…</p>}
          <div ref={visualHost} className="overflow-hidden rounded-md border" />
          <div className="flex gap-2">
            <Button type="button" onClick={propose} disabled={loading}>
              Review proposed HTML
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() => setMode("raw")}
            >
              Discard visual changes
            </Button>
          </div>
        </div>
      )}
      {renderPreview && mode === "raw" && (
        <div className="space-y-2">
          {dirty && (
            <p className="text-sm text-muted-foreground">
              Save changes to preview the current draft with the Go sending
              renderer.
            </p>
          )}
          <div className="flex flex-wrap items-end gap-2">
            <label className="text-sm">
              Real subscriber ID
              <Input
                type="number"
                min="1"
                value={subscriber}
                onChange={(e) => setSubscriber(e.target.value)}
              />
            </label>
            <Button
              type="button"
              variant="outline"
              onClick={rendered}
              disabled={loading || dirty}
            >
              Render subscriber preview
            </Button>
            {preview && (
              <Button
                type="button"
                variant="ghost"
                onClick={() => setPreview(null)}
              >
                Return to source preview
              </Button>
            )}
          </div>
          {preview && (
            <div className="text-sm">
              <p>Rendered subject: {preview.subject}</p>
              <details>
                <summary>Plain-text rendering</summary>
                <pre className="whitespace-pre-wrap">{preview.text}</pre>
              </details>
            </div>
          )}
        </div>
      )}
      <Dialog
        open={candidate !== null}
        onOpenChange={(open) => {
          if (!open) setCandidate(null);
        }}
      >
        <DialogContent style={{ width: "min(1200px, calc(100vw - 32px))" }}>
          <DialogHeader>
            <DialogTitle>Review full HTML changes</DialogTitle>
          </DialogHeader>
          <DialogDescription className="mb-4 text-sm">
            Original source is on the left. Proposed source is on the right.
            Check head styles, tables and conditional comments before applying.
            On smaller screens, scroll horizontally to compare both panes.
          </DialogDescription>
          {candidate !== null && (
            <HtmlChangeReview original={original} proposed={candidate} />
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setCandidate(null)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              onClick={() => {
                if (candidate !== null) onChange(candidate);
                setCandidate(null);
                setMode("raw");
              }}
            >
              Apply proposed HTML
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
