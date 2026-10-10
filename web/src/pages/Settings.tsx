import { useState } from "react";
import {
  ShieldCheck,
  Building2,
  Mail,
  Webhook,
  Ban,
  UsersRound,
  ListRestart,
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
export function SettingsPage({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const [tab, setTab] = useState("security");
  const icons: Record<string, LucideIcon> = {
    security: ShieldCheck,
    brand: Building2,
    messages: Mail,
    webhooks: Webhook,
    suppressions: Ban,
    users: UsersRound,
    jobs: ListRestart,
  };
  const descriptions: Record<string, string> = {
    security: "Protect your account and manage your sign-in methods.",
    brand: "Set the identity your subscribers see in their inbox.",
    messages: "Customize subscription pages and automated emails.",
    webhooks: "Connect brand events to your integrations.",
    suppressions: "Manage addresses and domains excluded from sending.",
    users: "Manage teammates, roles and access to brands.",
    jobs: "Review jobs that exhausted their automatic retries.",
  };
  const tabs = [
    ["security", "Account security"],
    ...(brandId
      ? [
          ["brand", "Brand"],
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
    <div className="space-y-6">
      <div className="page-heading">
        <div>
          <p className="eyebrow">Workspace</p>
          <h1>Settings</h1>
          <p className="page-subtitle">
            Manage your account, brand preferences and integrations.
          </p>
        </div>
      </div>
      <div className="settings-layout">
        <nav aria-label="Settings sections" className="settings-nav">
          {tabs.map(([id, label]) => {
            const Icon = icons[id];
            return (
              <Button
                key={id}
                variant="ghost"
                className={selected === id ? "active justify-start" : "justify-start"}
                size="sm"
                aria-current={selected === id ? "page" : undefined}
                onClick={() => setTab(id)}
              >
                <Icon size={17} aria-hidden="true" />
                {label}
              </Button>
            );
          })}
        </nav>
        <div className="settings-content space-y-5">
          <div className="space-y-1">
            <h2>{tabs.find(([id]) => id === selected)?.[1]}</h2>
            <p className="page-subtitle">{descriptions[selected]}</p>
          </div>
          {selected === "security" && <Security />}
          {selected === "brand" && (
            <BrandIdentity key={brandId} brandId={brandId} />
          )}
          {selected === "messages" && (
            <Messages key={brandId} brandId={brandId} />
          )}
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
