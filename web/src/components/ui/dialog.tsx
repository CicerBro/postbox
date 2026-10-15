import * as React from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "../../lib/utils";
export type DialogWidth = "sm" | "md" | "lg" | "xl";
export const Dialog = DialogPrimitive.Root;
export const DialogDescription = DialogPrimitive.Description;
export const DialogContent = React.forwardRef<
  React.ElementRef<typeof DialogPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content> & {
    showCloseButton?: boolean;
    width?: DialogWidth;
    onOverlayClick?: React.MouseEventHandler<HTMLDivElement>;
  }
>(({ className, children, showCloseButton = true, width, onOverlayClick, ...props }, ref) => (
  <DialogPrimitive.Portal>
    <DialogPrimitive.Overlay data-slot="dialog-overlay" className="dialog-overlay" onClick={onOverlayClick} />
    <DialogPrimitive.Content
      ref={ref}
      data-slot="dialog-content"
      className={cn("dialog-content", width && `dialog-width-${width}`, className)}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close data-slot="dialog-close" className="dialog-close" aria-label="Close dialog">
          <X size={18} aria-hidden="true" />
        </DialogPrimitive.Close>
      )}
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
));
DialogContent.displayName = "DialogContent";
export function DialogHeader({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="dialog-header" className={cn("dialog-header", className)} {...props} />;
}
export function DialogFooter({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return <div data-slot="dialog-footer" className={cn("dialog-footer", className)} {...props} />;
}
export const DialogTitle = DialogPrimitive.Title;
