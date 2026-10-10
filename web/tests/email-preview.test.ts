import { expect, test } from "bun:test";
import { QueryClient } from "@tanstack/react-query";
import { htmlPreviewRequest } from "../src/components/email-preview";

test("plain text override changes preserve the HTML request and cached preview", async () => {
  const client = new QueryClient();
  let requests = 0;
  const input = { brandId: 1, campaignId: 4, html: '<p>Hello {{ name }}</p>', inlineCSS: false, subscriberId: 12, subject: 'Hello', text: 'Original plain text' };
  async function preview(props: typeof input) {
    const request = htmlPreviewRequest(props);
    return client.fetchQuery({ queryKey: request.queryKey, staleTime: Infinity, queryFn: async () => { requests++; return { html: '<p>Hello Jane</p>', body: request.body }; } });
  }
  const original = await preview(input);
  const changed = await preview({ ...input, text: 'An unsaved text override {{ name }}' });
  expect(changed).toBe(original);
  expect(requests).toBe(1);
  expect(changed.body).not.toHaveProperty('text');
  await preview({ ...input, html: '<p>New HTML</p>' });
  expect(requests).toBe(2);
  await preview({ ...input, subscriberId: 13 });
  expect(requests).toBe(3);
  client.clear();
});
