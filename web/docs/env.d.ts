declare module "virtual:postbox-guides" {
  export const legacyRoutes: Record<string, string>;
  const guides: { id: string; title: string; group: string; html: string; text: string; source: string; toc: { id: string; title: string }[] }[];
  export default guides;
}
