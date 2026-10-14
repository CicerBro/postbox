import { useRef, useState, type ReactNode } from "react";
import { ErrorMessage } from "../RequestError";
import { Button } from "./button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  type DialogWidth,
} from "./dialog";

export type ConfirmDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description: ReactNode;
  onConfirm: () => void | Promise<unknown>;
  confirmLabel?: string;
  pendingLabel?: string;
  cancelLabel?: string;
  pending?: boolean;
  confirmDisabled?: boolean;
  variant?: "default" | "destructive";
  error?: unknown;
  width?: DialogWidth;
};

/** Callers close on success; failures stay open for review and retry. */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  onConfirm,
  confirmLabel = "Confirm",
  pendingLabel = "Working…",
  cancelLabel = "Cancel",
  pending = false,
  confirmDisabled = false,
  variant = "destructive",
  error,
  width = "md",
}: ConfirmDialogProps) {
  const running = useRef(false);
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<unknown>();
  const busy = pending || submitting;
  function changeOpen(next: boolean) {
    if (!pending && !running.current) {
      setLocalError(undefined);
      onOpenChange(next);
    }
  }
  async function confirm() {
    if (pending || running.current || confirmDisabled) return;
    running.current = true;
    setSubmitting(true);
    setLocalError(undefined);
    try {
      await onConfirm();
    } catch (failure) {
      setLocalError(failure);
    } finally {
      running.current = false;
      setSubmitting(false);
    }
  }
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent
        width={width}
        showCloseButton={!busy}
        aria-busy={busy}
        onOverlayClick={() => changeOpen(false)}
        onEscapeKeyDown={(event) => {
          if (pending || running.current) event.preventDefault();
        }}
        onPointerDownOutside={(event) => {
          if (pending || running.current) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (pending || running.current) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <ErrorMessage error={localError ?? error} presentation="alert" />
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => changeOpen(false)}
          >
            {cancelLabel}
          </Button>
          <Button
            type="button"
            variant={variant}
            disabled={busy || confirmDisabled}
            onClick={() => void confirm()}
          >
            {busy ? pendingLabel : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type Confirmation = Pick<
  ConfirmDialogProps,
  | "title"
  | "description"
  | "onConfirm"
  | "confirmLabel"
  | "pendingLabel"
  | "variant"
  | "width"
>;

/** Opens a confirmation from an action button while retaining its action state. */
export function useConfirmDialog({
  pending,
  error,
}: {
  pending: boolean;
  error?: unknown;
}) {
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null);
  const [attempted, setAttempted] = useState(false);
  function requestConfirmation(options: Confirmation) {
    setAttempted(false);
    setConfirmation(options);
  }
  function closeConfirmation() {
    setConfirmation(null);
  }
  const confirmationDialog = confirmation && (
    <ConfirmDialog
      width="sm"
      {...confirmation}
      open
      pending={pending}
      error={attempted ? error : undefined}
      onOpenChange={(open) => {
        if (!open) closeConfirmation();
      }}
      onConfirm={() => {
        setAttempted(true);
        return confirmation.onConfirm();
      }}
    />
  );
  return { requestConfirmation, closeConfirmation, confirmationDialog };
}
