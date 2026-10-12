import { Link, Navigate, useParams } from "react-router-dom";
import { useApp } from "../lib/app-context";
import { BrandIdentity } from "../components/settings/BrandIdentity";
import { Security } from "../components/settings/Security";
import { Messages } from "../components/settings/Messages";
import { Webhooks } from "../components/settings/Webhooks";
import { Suppressions } from "../components/settings/Suppressions";
import { Users } from "../components/settings/Users";
import { Jobs, JobRetention } from "../components/settings/Jobs";
import { AmazonSES } from "../components/settings/AmazonSES";
import { PublicSubscriptions } from "../components/settings/PublicSubscriptions";
import { ProfilePreferences } from "../components/settings/ProfilePreferences";
import { TimezonePreferences } from "../components/settings/TimezonePreferences";
import {
  DeploymentSettings,
  SendingControl,
} from "../components/settings/Administration";
import { DomainsSettings } from "./Domains";
import { ApiKeysPage } from "./ApiKeys";

type Area = "account" | "brand" | "admin";
const sections = {
  account: [
    ["security", "Account security"],
    ["preferences", "Preferences"],
  ],
  brand: [
    ["general", "General"],
    ["sending", "Sending & domains"],
    ["subscriptions", "Subscriptions"],
    ["integrations", "Integrations"],
    ["danger", "Danger zone"],
  ],
  admin: [
    ["users", "Users & access"],
    ["policies", "Application policies"],
    ["operations", "Operations"],
    ["suppressions", "Global suppressions"],
    ["deployment", "Deployment"],
  ],
};
export function SettingsPage({
  brandId = 0,
  area = "account",
}: {
  brandId?: number;
  area?: Area;
}) {
  const { session, brands } = useApp();
  const { section } = useParams();
  const brand = brands.find((item) => item.id === brandId);
  const tabs = sections[area].filter(
    ([id]) => id !== "danger" || session.admin,
  );
  const base =
    area === "brand"
      ? `/brands/${brandId}/settings`
      : area === "admin"
        ? "/admin"
        : "/account";
  if (area === "admin" && !session.admin)
    return (
      <div className="empty-panel">
        <h1>Administrator access required</h1>
        <p>Application settings are managed by administrators.</p>
      </div>
    );
  if (area === "brand" && !brand)
    return (
      <div className="empty-panel">
        <h1>Brand unavailable</h1>
        <p>Select a brand you can access.</p>
      </div>
    );
  if (!section) return <Navigate replace to={`${base}/${tabs[0][0]}`} />;
  if (!tabs.some(([id]) => id === section))
    return (
      <div className="empty-panel">
        <h1>Settings section unavailable</h1>
        <Link to={`${base}/${tabs[0][0]}`}>Open settings</Link>
      </div>
    );
  const title =
    area === "account"
      ? "My account"
      : area === "admin"
        ? "Administration"
        : `${brand?.name} settings`;
  return (
    <div>
      <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
      <p className="page-subtitle">
        {area === "account"
          ? "Your sign-in methods and personal preferences."
          : area === "admin"
            ? "Applies to all brands. Only administrators can manage these settings."
            : `Applies to ${brand?.name}.`}
      </p>
      <div role="separator" className="my-4 border-t lg:my-6" />
      <div className="flex flex-col gap-4 lg:flex-row lg:gap-12">
        <nav
          aria-label={`${title} sections`}
          className="flex shrink-0 gap-2 overflow-x-auto py-2 lg:w-1/5 lg:flex-col"
        >
          {tabs.map(([id, label]) => (
            <Link
              key={id}
              to={`${base}/${id}`}
              aria-current={section === id ? "page" : undefined}
              className={`settings-nav-button rounded-md px-3 py-2 text-sm ${section === id ? "bg-muted font-medium" : "hover:bg-accent"}`}
            >
              {label}
            </Link>
          ))}
        </nav>
        <div
          className={`min-w-0 flex-1 space-y-6 p-1 ${area === "account" ? "[&>section]:max-w-xl [&_form]:max-w-xl" : section === "general" || section === "danger" ? "[&>section]:max-w-xl [&_form]:max-w-xl" : ""}`}
          key={`${area}:${brandId}:${section}`}
        >
          <h2 className="border-b pb-4 text-lg font-medium">
            {tabs.find(([id]) => id === section)?.[1]}
          </h2>
          {area === "account" && section === "security" && <Security />}
          {area === "account" && section === "preferences" && (
            <>
              <ProfilePreferences />
              <TimezonePreferences />
            </>
          )}
          {area === "brand" && section === "general" && (
            <BrandIdentity brandId={brandId} />
          )}
          {area === "brand" && section === "sending" && (
            <>
              <BrandIdentity brandId={brandId} section="sending" />
              <AmazonSES brandId={brandId} />
              <DomainsSettings brandId={brandId} />
            </>
          )}
          {area === "brand" && section === "subscriptions" && (
            <>
              <PublicSubscriptions brandId={brandId} scope="brand" />
              <Messages brandId={brandId} />
            </>
          )}
          {area === "brand" && section === "integrations" && (
            <>
              <Webhooks brandId={brandId} />
              {session.admin && <ApiKeysPage brandId={brandId} embedded />}
            </>
          )}
          {area === "brand" && section === "danger" && (
            <BrandIdentity brandId={brandId} section="danger" />
          )}
          {area === "admin" && section === "users" && <Users />}
          {area === "admin" && section === "policies" && (
            <>
              <PublicSubscriptions scope="global" />
              <JobRetention />
            </>
          )}
          {area === "admin" && section === "operations" && (
            <>
              <SendingControl />
              <Jobs />
            </>
          )}
          {area === "admin" && section === "suppressions" && (
            <Suppressions scope="global" />
          )}
          {area === "admin" && section === "deployment" && (
            <DeploymentSettings />
          )}
        </div>
      </div>
    </div>
  );
}
