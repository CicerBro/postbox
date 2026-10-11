import { CircleAlert } from 'lucide-react';
import { Alert, AlertDescription } from './ui/alert';

export const errorText = (error: unknown) => error instanceof Error ? error.message : String(error ?? 'Request failed');
export function ErrorMessage({ error, className = 'text-sm text-destructive', presentation = 'inline' }: { error: unknown; className?: string; presentation?: 'inline' | 'alert' }) {
  if (!error) return null;
  const forbidden = typeof error === 'object' && 'status' in error && error.status === 403;
  const message = <>{forbidden ? 'Access denied (403): ' : ''}{errorText(error)}</>;
  if (presentation === 'alert') return (
    <Alert className="border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
      <CircleAlert size={16} className="mt-1 shrink-0" aria-hidden="true" />
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
  return <p role="alert" className={className}>{message}</p>;
}
