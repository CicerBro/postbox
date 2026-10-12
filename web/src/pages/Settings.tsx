import { useState } from "react";
import {
  ShieldCheck,
  Building2,
  Mail,
  Webhook,
  Ban,
  UsersRound,
  ListRestart,
  Cloud,
  UserPlus,
  Globe,
  type LucideIcon,
} from "lucide-react";
import { useApp } from "../lib/app-context";
import { Button } from "../components/ui/button";
import { BrandIdentity } from "../components/settings/BrandIdentity";
import { Security } from "../components/settings/Security";
import { Messages } from "../components/settings/Messages";
import { Webhooks } from "../components/settings/Webhooks";
import { Suppressions } from "../components/settings/Suppressions";
import { Users } from "../components/settings/Users";
import { Jobs } from "../components/settings/Jobs";
import { AmazonSES } from "../components/settings/AmazonSES";
import { PublicSubscriptions } from "../components/settings/PublicSubscriptions";
import { TimezonePreferences } from "../components/settings/TimezonePreferences";
export function SettingsPage({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const [tab, setTab] = useState("security");
  const icons: Record<string, LucideIcon> = {
    security: ShieldCheck,
    preferences: Globe,
    brand: Building2,
    messages: Mail,
    webhooks: Webhook,
    suppressions: Ban,
    users: UsersRound,
    jobs: ListRestart,
    ses: Cloud,
    subscriptions: UserPlus,
  };
  const descriptions: Record<string, string> = {
    security: "Protect your account and manage your sign-in methods.",
    preferences: "Choose how dates and times appear across your workspace.",
    brand: "Set the identity your subscribers see in their inbox.",
    messages: "Customize subscription pages and automated emails.",
    webhooks: "Connect brand events to your integrations.",
    suppressions: "Manage addresses and domains excluded from sending.",
    users: "Manage teammates, roles and access to brands.",
    jobs: "Review jobs that exhausted their automatic retries.",
    ses: "Connect this brand to Amazon SES and inspect its current sending quota.",
    subscriptions: "Control who can sign up through public pages and embedded forms.",
  };
  const tabs = [
    ["security", "Account security"],
    ["preferences", "Preferences"],
    ...(brandId || session.admin ? [["subscriptions", "Public subscriptions"]] : []),
    ...(brandId
      ? [
          ["brand", "Brand"],
          ["ses", "Amazon SES"],
          ["messages", "Messages"],
          ["webhooks", "Webhooks"],
          ["suppressions", "Suppressions"],
        ]
      : []),
    ...(session.admin
      ? [
          ["users", "Users"],
          ["jobs", "Failed jobs"],
        ]
      : []),
  ];
  const selected = tabs.some(([id]) => id === tab) ? tab : "security";
  return (
    <div>
      <div className="space-y-0.5">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Settings</h1>
          <p className="page-subtitle">
            Manage your account, brand preferences and integrations.
          </p>
        </div>
      </div>
      <div role="separator" className="my-4 border-t lg:my-6" />
      <div className="flex flex-col gap-2 lg:flex-row lg:gap-12">
        <nav
          aria-label="Settings sections"
          className="flex w-full shrink-0 gap-2 overflow-x-auto px-1 py-2 lg:w-1/5 lg:flex-col lg:gap-1"
        >
          {tabs.map(([id, label]) => {
            const Icon = icons[id];
            return (
              <Button
                key={id}
                variant="ghost"
                className={
                  selected === id
                    ? "settings-nav-button justify-start bg-muted hover:bg-accent"
                    : "settings-nav-button justify-start hover:underline"
                }
                size="default"
                aria-current={selected === id ? "page" : undefined}
                onClick={() => setTab(id)}
              >
                <Icon size={18} aria-hidden="true" />
                {label}
              </Button>
            );
          })}
        </nav>
        <div
          className={`min-w-0 flex-1 space-y-6 p-1 ${
            selected === "messages" || selected === "jobs"
              ? ""
              : selected === "webhooks"
                ? "[&_form]:max-w-3xl"
                : "[&_form]:max-w-xl"
          } ${
            selected === "security" || selected === "brand"
              ? "[&>section]:max-w-xl"
              : ""
          }`}
        >
          <div className="space-y-1 border-b pb-4">
            <h2 className="text-lg font-medium">
              {tabs.find(([id]) => id === selected)?.[1]}
            </h2>
            <p className="page-subtitle">{descriptions[selected]}</p>
          </div>
          {selected === "security" && <Security />}
          {selected === "preferences" && <TimezonePreferences />}
          {selected === "subscriptions" && <PublicSubscriptions key={brandId} brandId={brandId} />}
          {selected === "brand" && (
            <BrandIdentity key={brandId} brandId={brandId} />
          )}
          {selected === "messages" && (
            <Messages key={brandId} brandId={brandId} />
          )}
          {selected === "ses" && <AmazonSES key={brandId} brandId={brandId} />}
          {selected === "webhooks" && (
            <Webhooks key={brandId} brandId={brandId} />
          )}
          {selected === "suppressions" && (
            <Suppressions key={brandId} brandId={brandId} />
          )}
          {selected === "users" && <Users />}
          {selected === "jobs" && <Jobs />}
        </div>
      </div>
      {!brandId && (
        <p className="text-sm text-muted-foreground">
          Select or create a brand to manage identity, messages, webhooks and
          suppressions.
        </p>
      )}
    </div>
  );
}
