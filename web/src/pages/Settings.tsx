import { useState } from "react";
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
      <h1 className="text-2xl font-semibold">Settings</h1>
      <nav aria-label="Settings sections" className="flex flex-wrap gap-2">
        {tabs.map(([id, label]) => (
          <Button
            key={id}
            variant={selected === id ? "default" : "outline"}
            size="sm"
            aria-current={selected === id ? "page" : undefined}
            onClick={() => setTab(id)}
          >
            {label}
          </Button>
        ))}
      </nav>
      {selected === "security" && <Security />}
      {selected === "brand" && (
        <BrandIdentity key={brandId} brandId={brandId} />
      )}
      {selected === "messages" && <Messages key={brandId} brandId={brandId} />}
      {selected === "webhooks" && <Webhooks key={brandId} brandId={brandId} />}
      {selected === "suppressions" && (
        <Suppressions key={brandId} brandId={brandId} />
      )}
      {selected === "users" && <Users />}
      {selected === "jobs" && <Jobs />}
      {!brandId && (
        <p className="text-sm text-muted-foreground">
          Select or create a brand to manage identity, messages, webhooks and
          suppressions.
        </p>
      )}
    </div>
  );
}
