import { api } from './api';
export async function allPages<T extends { id: number }>(path: string, brandId: number, signal?: AbortSignal): Promise<T[]> {
  const result: T[] = [];
  let after = 0;
  for (;;) {
    const page = await api<T[]>(`${path}${path.includes('?') ? '&' : '?'}limit=500&after=${after}`, { brandId, signal });
    result.push(...(page ?? []));
    if (!page || page.length < 500) return result;
    const next = page.at(-1)!.id;
    if (!Number.isSafeInteger(next) || next <= after) throw new Error('Invalid pagination cursor');
    after = next;
  }
}
