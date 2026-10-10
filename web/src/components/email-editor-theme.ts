import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags } from "@lezer/highlight";

// CSS variables follow the app's theme without recreating the editor or its
// selection, undo history, and unsaved document when the theme changes.
const editorTheme = EditorView.theme({
  "&": {
    color: "var(--foreground)",
    backgroundColor: "var(--background)",
    fontWeight: "400",
    "--editor-tag": "#005cc5",
    "--editor-attribute": "#6f42c1",
    "--editor-string": "#176b36",
    "--editor-keyword": "#a626a4",
    "--editor-number": "#986000",
    "--editor-comment": "#626874",
    "--editor-invalid": "#c62828",
    "--editor-deleted": "#ffe9e9",
    "--editor-inserted": "#e6f5e9",
    "--editor-deleted-text": "#ffd0d0",
    "--editor-inserted-text": "#c8ebce",
  },
  ".dark &": {
    "--editor-tag": "#82baff",
    "--editor-attribute": "#c6a0ff",
    "--editor-string": "#95d9a5",
    "--editor-keyword": "#e6a0e5",
    "--editor-number": "#f5c778",
    "--editor-comment": "#a1a8b5",
    "--editor-invalid": "#ff9c9c",
    "--editor-deleted": "#392329",
    "--editor-inserted": "#20372b",
    "--editor-deleted-text": "#583139",
    "--editor-inserted-text": "#2b5038",
  },
  ".cm-scroller": {
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
  },
  ".cm-content": { caretColor: "var(--foreground)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--foreground)" },
  ".cm-gutters": {
    color: "var(--muted-foreground)",
    backgroundColor: "var(--muted-background)",
    borderRightColor: "var(--border)",
  },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, .cm-content ::selection": {
    backgroundColor: "var(--accent)",
  },
  "&.cm-merge-a .cm-changedLine": { backgroundColor: "var(--editor-deleted)" },
  "&.cm-merge-b .cm-changedLine": { backgroundColor: "var(--editor-inserted)" },
  "&.cm-merge-a .cm-changedText": { backgroundColor: "var(--editor-deleted-text)" },
  "&.cm-merge-b .cm-changedText": { backgroundColor: "var(--editor-inserted-text)" },
});

const highlightStyle = HighlightStyle.define([
  { tag: [tags.tagName, tags.typeName], color: "var(--editor-tag)" },
  { tag: [tags.attributeName, tags.propertyName, tags.variableName], color: "var(--editor-attribute)" },
  { tag: [tags.string, tags.attributeValue], color: "var(--editor-string)" },
  { tag: [tags.keyword, tags.operator], color: "var(--editor-keyword)" },
  { tag: [tags.number, tags.bool, tags.unit], color: "var(--editor-number)" },
  { tag: [tags.comment, tags.meta], color: "var(--editor-comment)" },
  { tag: [tags.angleBracket, tags.punctuation], color: "var(--muted-foreground)" },
  { tag: tags.invalid, color: "var(--editor-invalid)" },
]);

export const emailEditorTheme = [editorTheme, syntaxHighlighting(highlightStyle)];
