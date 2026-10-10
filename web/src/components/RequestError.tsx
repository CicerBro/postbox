export const errorText = (error: unknown) => error instanceof Error ? error.message : String(error ?? 'Request failed');
export function ErrorMessage({ error, className = 'text-sm text-destructive' }: { error: unknown; className?: string }) {
  if (!error) return null;
  const forbidden = typeof error === 'object' && 'status' in error && error.status === 403;
  return <p role="alert" className={className}>{forbidden ? 'Access denied (403): ' : ''}{errorText(error)}</p>;
}
