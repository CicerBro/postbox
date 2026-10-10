// HTML rendering has independent dependencies from the optional text override.
export function htmlPreviewRequest(input: {
  brandId: number;
  campaignId: number;
  html: string;
  inlineCSS: boolean;
  subscriberId: number;
  subject: string;
  text?: string;
}) {
  return {
    queryKey: ["editor-preview", input.brandId, input.campaignId, input.html, input.inlineCSS, input.subscriberId, input.subject],
    body: { html: input.html, inline_css: input.inlineCSS, subscriber_id: input.subscriberId, campaign_id: input.campaignId, subject: input.subject },
  };
}
