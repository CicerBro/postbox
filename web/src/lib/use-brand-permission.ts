import { useApp, type Brand } from "./app-context";

export function useBrandPermission(brandId: number, permission: NonNullable<Brand["permissions"]>[number]) {
  const { session, brands } = useApp();
  return session.admin || !!brands.find((brand) => brand.id === brandId)?.permissions?.includes(permission);
}
