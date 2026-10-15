import { lazy, Suspense, type ComponentProps } from "react";
import type { EmailEditor as EditorComponent } from "./EmailEditor";

const Editor = lazy(() => import("./EmailEditor").then((module) => ({ default: module.EmailEditor })));

export function EmailEditor(props: ComponentProps<typeof EditorComponent>) {
  return (
    <Suspense fallback={<p role="status">Loading email editor…</p>}>
      <Editor {...props} />
    </Suspense>
  );
}
