import { useConfirmDialog } from "../ui/confirm-dialog";
import { useEffect, useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { Badge } from "../ui/badge";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { SelectField } from "../ui/select";
import { Section, Field, Message, useAction, Checks, strings } from "./Common";
type User = {
  id: number;
  email: string;
  display_name: string;
  role: string;
  totp_enabled: boolean | number;
};
type Grant = {
  brand_id: number;
  permissions: string | string[];
  daily_send_limit: number | null;
};
export function Users() {
  const { session, logout } = useApp();
  const qc = useQueryClient();
  const [editing, setEditing] = useState<User | null>();
  const a = useAction();
  const { requestConfirmation, closeConfirmation, confirmationDialog } =
    useConfirmDialog({ pending: a.busy, error: a.error });
  const q = useInfiniteQuery({
    queryKey: ["users"],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api<User[]>(`/users?after=${pageParam}&limit=100`),
    getNextPageParam: (rows) =>
      rows.length === 100 ? rows.at(-1)?.id : undefined,
  });
  return (
    <Section>
      <p className="text-sm text-muted-foreground">
        Administrators can manage every brand. Brand users need explicit read,
        write and send grants. The server prevents removal of the last
        administrator. Changes sign out the affected user; deleting a user also
        cancels their unfinished campaigns.
      </p>
      <Button variant="outline" onClick={() => setEditing(null)}>
        <Plus size={15} aria-hidden="true" />
        Create user
      </Button>
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading users…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : (
        <ul className="striped-list space-y-3">
          {q.data?.pages.flat().map((u) => (
            <li
              key={u.id}
              className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3"
            >
              <div className="flex items-center gap-3 text-sm">
                <span className="avatar">
                  {u.email.slice(0, 2).toUpperCase()}
                </span>
                <div>
                  <p className="row-title">
                    {u.email}
                    {u.id === session.user_id ? " (you)" : ""}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    <Badge variant={u.role === "admin" ? "info" : "secondary"}>
                      {u.role === "admin" ? "Administrator" : "Brand user"}
                    </Badge>{" "}
                    <span className="ml-2">
                      Authenticator {u.totp_enabled ? "enabled" : "off"}
                    </span>
                  </p>
                </div>
              </div>
              <div className="campaign-row-actions">
                <Button
                  size="icon"
                  variant="ghost"
                  className="action-edit"
                  aria-label={`Edit access for ${u.email}`}
                  title="Edit access"
                  onClick={() => setEditing(u)}
                >
                  <Pencil size={16} aria-hidden="true" />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  className="campaign-delete-action"
                  aria-label={`Delete ${u.email}`}
                  title="Delete user"
                  disabled={a.busy}
                  onClick={() => {
                    requestConfirmation({
                      title: "Delete user?",
                      description: `Delete ${u.email}? Their unfinished campaigns will be cancelled.`,
                      confirmLabel: "Delete user",
                      onConfirm: () =>
                        a.run(async () => {
                          await api(`/users/${u.id}`, { method: "DELETE" });
                          if (u.id === session.user_id) await logout();
                          await qc.invalidateQueries({ queryKey: ["users"] });

                          closeConfirmation();
                        }, "User deleted."),
                    });
                  }}
                >
                  <Trash2 size={16} aria-hidden="true" />
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {q.hasNextPage && (
        <Button
          variant="outline"
          disabled={q.isFetchingNextPage}
          onClick={() => q.fetchNextPage()}
        >
          Load more users
        </Button>
      )}
      <Message {...a} />
      {editing !== undefined && (
        <UserEditor
          key={editing?.id ?? "new"}
          user={editing}
          onClose={() => setEditing(undefined)}
        />
      )}
      {confirmationDialog}
    </Section>
  );
}
function UserEditor({
  user,
  onClose,
}: {
  user: User | null;
  onClose: () => void;
}) {
  const { session, logout } = useApp();
  const qc = useQueryClient();
  const a = useAction();
  const { requestConfirmation, closeConfirmation, confirmationDialog } =
    useConfirmDialog({ pending: a.busy, error: a.error });
  const [displayName, setDisplayName] = useState(user?.display_name ?? "");
  const [email, setEmail] = useState(user?.email ?? "");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState(user?.role ?? "brand_user");
  const [adminPassword, setAdminPassword] = useState("");
  return (
    <div className="rounded-xl border bg-muted/20 p-5 space-y-5">
      <h3 className="font-medium">{user ? "Edit user" : "Create user"}</h3>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void a.run(
            async () => {
              await api(user ? `/users/${user.id}` : "/users", {
                method: user ? "PATCH" : "POST",
                body: {
                  email,
                  display_name: displayName,
                  password: password || undefined,
                  role,
                },
              });
              setPassword("");
              await qc.invalidateQueries({ queryKey: ["users"] });
              if (user?.id === session.user_id) await logout();
              onClose();
            },
            user ? "User updated." : "User created.",
          );
        }}
      >
        <Field
          label="Display name"
          value={displayName}
          maxLength={100}
          placeholder="Defaults to email"
          onChange={(e) => setDisplayName(e.target.value)}
        />
        <Field
          label="Email"
          type="email"
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <Field
          label={
            user ? "New password (leave blank to retain)" : "Initial password"
          }
          type="password"
          autoComplete="new-password"
          minLength={8}
          aria-describedby="user-password-hint"
          required={!user}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <p id="user-password-hint" className="text-sm text-muted-foreground">
          At least 8 characters.
        </p>
        <label htmlFor="user-role" className="block text-sm space-y-2">
          Role
          <SelectField
            id="user-role"
            value={role}
            onValueChange={setRole}
            options={[
              { value: "brand_user", label: "Brand user" },
              { value: "admin", label: "Administrator" },
            ]}
          />
        </label>
        <div className="flex gap-2">
          <Button disabled={a.busy}>
            {user ? "Save user" : "Create user"}
          </Button>
          <Button type="button" variant="ghost" onClick={onClose}>
            Close
          </Button>
        </div>
      </form>
      {user && (
        <>
          <BrandAccess userId={user.id} />
          {!!user.totp_enabled && (
            <div className="border-t pt-4 space-y-3">
              <p className="text-sm">
                Reset this user’s authenticator if they have lost access.
                Confirm your own administrator password.
              </p>
              <Field
                label="Your current password"
                type="password"
                autoComplete="current-password"
                value={adminPassword}
                onChange={(e) => setAdminPassword(e.target.value)}
              />
              <Button
                variant="outline"
                disabled={a.busy || !adminPassword}
                onClick={() => {
                  requestConfirmation({
                    title: "Reset authenticator?",
                    description: `Reset the authenticator for ${user.email}? All their sessions will be signed out.`,
                    confirmLabel: "Reset authenticator",
                    onConfirm: () =>
                      a.run(async () => {
                        await api(`/users/${user.id}/totp`, {
                          method: "DELETE",
                          body: { password: adminPassword },
                        });
                        setAdminPassword("");
                        await qc.invalidateQueries({ queryKey: ["users"] });
                        if (user.id === session.user_id) await logout();
                        onClose();

                        closeConfirmation();
                      }, "Authenticator reset."),
                  });
                }}
              >
                Reset authenticator
              </Button>
            </div>
          )}
        </>
      )}
      <Message {...a} />
      {confirmationDialog}
    </div>
  );
}
function BrandAccess({ userId }: { userId: number }) {
  const qc = useQueryClient();
  const a = useAction();
  const { requestConfirmation, closeConfirmation, confirmationDialog } =
    useConfirmDialog({ pending: a.busy, error: a.error });
  const brands = useInfiniteQuery({
    queryKey: ["access-brands"],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api<{ id: number; name: string }[]>(
        `/brands?after=${pageParam}&limit=100`,
      ),
    getNextPageParam: (rows) =>
      rows.length === 100 ? rows.at(-1)?.id : undefined,
  });
  const grants = useQuery({
    queryKey: ["user-brands", userId],
    queryFn: async () => {
      let after = 0;
      const out: Grant[] = [];
      while (true) {
        const rows = await api<Grant[]>(
          `/users/${userId}/brands?after=${after}&limit=500`,
        );
        out.push(...rows);
        if (rows.length < 500) break;
        after = rows[rows.length - 1].brand_id;
      }
      return out;
    },
  });
  const [brandId, setBrandId] = useState(0);
  const [permissions, setPermissions] = useState(["read"]);
  const [limit, setLimit] = useState("");
  useEffect(() => {
    const g = grants.data?.find((g) => g.brand_id === brandId);
    setPermissions(g ? strings(g.permissions) : ["read"]);
    setLimit(
      g?.daily_send_limit === null || g?.daily_send_limit === undefined
        ? ""
        : String(g.daily_send_limit),
    );
  }, [brandId, grants.data]);
  return (
    <div className="border-t pt-4 space-y-4">
      <h4 className="text-sm font-medium">Brand grants</h4>
      <Message error={brands.error || grants.error} />
      {grants.isPending ? (
        <p role="status">Loading grants…</p>
      ) : !grants.data?.length ? (
        <p className="text-sm">No brand grants.</p>
      ) : (
        <ul className="space-y-2 text-sm">
          {grants.data.map((g) => (
            <li
              className="flex flex-wrap items-center justify-between gap-2"
              key={g.brand_id}
            >
              <span>
                {brands.data?.pages.flat().find((b) => b.id === g.brand_id)
                  ?.name ?? `Brand ${g.brand_id}`}{" "}
                · {strings(g.permissions).join(", ")} · Daily limit:{" "}
                {g.daily_send_limit ?? "Unlimited"}
              </span>
              <Button
                variant="destructive"
                size="sm"
                disabled={a.busy}
                onClick={() => {
                  requestConfirmation({
                    title: "Remove grant?",
                    description: "Remove this brand grant?",
                    confirmLabel: "Remove grant",
                    onConfirm: () =>
                      a.run(async () => {
                        await api(`/users/${userId}/brands/${g.brand_id}`, {
                          method: "DELETE",
                        });
                        await qc.invalidateQueries({
                          queryKey: ["user-brands", userId],
                        });

                        closeConfirmation();
                      }, "Access removed."),
                  });
                }}
              >
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void a.run(async () => {
            await api(`/users/${userId}/brands/${brandId}`, {
              method: "PUT",
              body: {
                permissions,
                daily_send_limit: limit === "" ? null : Number(limit),
              },
            });
            await qc.invalidateQueries({ queryKey: ["user-brands", userId] });
          }, "Brand access saved.");
        }}
      >
        <label htmlFor="user-brand" className="block text-sm space-y-2">
          Brand
          <SelectField
            id="user-brand"
            required
            value={String(brandId)}
            onValueChange={(value) => setBrandId(Number(value))}
            options={[
              { value: "0", label: "Select a brand" },
              ...(brands.data?.pages
                .flat()
                .map((b) => ({ value: String(b.id), label: b.name })) ?? []),
            ]}
          />
        </label>
        {brands.hasNextPage && (
          <Button
            type="button"
            variant="outline"
            disabled={brands.isFetchingNextPage}
            onClick={() => brands.fetchNextPage()}
          >
            Load more brands
          </Button>
        )}
        <Checks
          values={["read", "write", "send"]}
          selected={permissions}
          onChange={setPermissions}
        />
        <Field
          label="Daily send limit (blank for unlimited)"
          type="number"
          min={0}
          step={1}
          value={limit}
          onChange={(e) => setLimit(e.target.value)}
        />
        <Button disabled={a.busy || !brandId || !permissions.length}>
          Save grant
        </Button>
      </form>
      <Message {...a} />
      {confirmationDialog}
    </div>
  );
}
