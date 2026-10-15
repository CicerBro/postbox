import { useEffect, useState, type ComponentProps } from "react";
import type { EmailEditor as EditorComponent } from "./EmailEditor";
import { Button } from "./ui/button";

let loadedEditor: typeof EditorComponent | null = null;
let editorImport: Promise<typeof EditorComponent> | undefined;

function loadEditor() {
  return editorImport ??= import("./EmailEditor")
    .then((module) => (loadedEditor = module.EmailEditor));
}

export function EmailEditor(props: ComponentProps<typeof EditorComponent>) {
  const [Editor, setEditor] = useState(() => loadedEditor);
  const [failed, setFailed] = useState(false);
  // Render as soon as the import resolves, without Suspense's reveal throttle.
  useEffect(() => {
    let mounted = true;
    void loadEditor().then(
      (component) => { if (mounted) setEditor(() => component); },
      () => { if (mounted) setFailed(true); },
    );
    return () => { mounted = false; };
  }, []);

  if (Editor) return <Editor {...props} />;
  if (failed) return (
    <div role="alert" className="space-y-2">
      <p>Could not load the email editor.</p>
      <Button type="button" variant="outline" onClick={() => window.location.reload()}>
        Reload page
      </Button>
    </div>
  );
  return <p role="status">Loading email editor…</p>;
}
