import { Field } from "../ui/field";
import { ConfirmDialog } from "../ui/confirm-dialog";
import { useDateTime } from "../../lib/date-time";
import { StatusMessage } from "../StatusMessage";
import { SelectField } from "../ui/select";
import { Checkbox } from "../ui/checkbox";
import { Switch } from "../ui/switch";
import { useEffect, useRef, useState } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "../ui/dialog";
import { download } from "../../lib/api";
import {
  type SubscriberList,
  type CustomField,
  ErrorMessage,
  parseCSV,
  tableClass,
  useSubscriberAction,
  useSubscribersQuery,
} from "./shared";

type ImportResult = {
  rows: number;
  imported: number;
  invalid: number;
  errors?: string[];
  preview?: {
    email: string;
    name: string;
    status: string;
    fields: Record<string, unknown>;
  }[];
};
export function CSVImport({
  brandId,
  lists,
  fields,
}: {
  brandId: number;
  lists: SubscriberList[];
  fields: CustomField[];
}) {
  const [file, setFile] = useState<File>();
  const [directImport, setDirectImport] = useState(false);
  const [csvText, setCSVText] = useState("");
  const inputVersion = useRef(0);
  const [headers, setHeaders] = useState<string[]>([]);
  const [mapping, setMapping] = useState<Record<string, string>>({});
  const [listIds, setListIds] = useState<number[]>([]);
  const [preview, setPreview] = useState<ImportResult>();
  const [result, setResult] = useState<ImportResult>();
  const [error, setError] = useState<unknown>();
  const [confirm, setConfirm] = useState(false);
  const action = useSubscriberAction<ImportResult>(brandId);
  const resetPreview = () => {
    setPreview(undefined);
    setResult(undefined);
    setError(undefined);
    action.reset();
  };
  const options = { mapping, list_ids: listIds };
  const loadCSV = (source: File, text: string) => {
    if (source.size > 1024 ** 3) throw new Error("The CSV limit is 1 GiB.");
    const first = parseCSV(text, 1)[0];
    if (!first?.length || !first.some((header) => header.trim()))
      throw new Error("CSV must have a header.");
    if (new Set(first).size !== first.length)
      throw new Error("CSV headers must be unique.");
    setHeaders(first);
    setMapping((previous) => Object.fromEntries(
      first.map((header) => {
        const normalized = header.replace(/^\uFEFF/, "").trim().toLowerCase();
        const destination = [
          "email", "name", "status", "fields", "gdpr_consent_at",
        ].includes(normalized) ? normalized : "";
        return [header, previous[header] ?? destination];
      }),
    ));
    setFile(source);
  };
  const resetInput = () => {
    inputVersion.current++;
    resetPreview();
    setFile(undefined);
    setHeaders([]);
    setMapping({});
  };
  const loadDirectCSV = (text: string) => {
    setFile(undefined);
    setHeaders([]);
    if (!text.trim()) return;
    try {
      loadCSV(new File([text], "pasted.csv", { type: "text/csv" }), text);
    } catch (err) {
      setError(err);
    }
  };
  const run = async (dry: boolean) => {
    if (!file) return;
    try {
      const v = await action.mutateAsync({
        path: `/subscribers/import${dry ? "/preview" : ""}?options=${encodeURIComponent(JSON.stringify(options))}`,
        body: file,
      });
      if (dry) setPreview(v);
      else {
        setResult(v);
        setPreview(undefined);
        setConfirm(false);
      }
    } catch {
      if (!dry) {
        setPreview(undefined);
        setConfirm(false);
        setError(
          new Error(
            "Import stopped. Earlier batches may have committed. Review subscribers before running another preview.",
          ),
        );
      }
    }
  };
  return (
    <section className="rounded-xl border bg-card p-6 shadow-sm space-y-4">
      <div>
        <h2 className="section-heading text-lg font-semibold">Import CSV</h2>
        <p className="text-sm text-muted-foreground">
          Map columns, validate without writing, then commit. Existing opt-outs
          and suppressed statuses are preserved.
        </p>
      </div>
      <label className="flex items-center gap-3 text-sm font-medium">
        <Switch
          checked={directImport}
          disabled={action.isPending}
          onCheckedChange={(checked) => {
            resetInput();
            setDirectImport(checked);
            if (checked) loadDirectCSV(csvText);
          }}
        />
        Paste CSV directly
      </label>
      {directImport ? (
        <div className="space-y-2">
          <Field label="CSV text">
            <Textarea
              aria-label="CSV text"
              rows={8}
              className="font-mono text-sm"
              placeholder={"email,name\nada@example.org,Ada"}
              value={csvText}
              disabled={action.isPending}
              onChange={(event) => {
                resetPreview();
                const text = event.target.value;
                setCSVText(text);
                loadDirectCSV(text);
              }}
            />
          </Field>
          <p className="text-xs text-muted-foreground">
            Include a header row. Column mapping and preview work the same as a CSV file.
          </p>
        </div>
      ) : (
        <Field label="CSV file">
          <Input
            type="file"
            accept=".csv,text/csv"
            disabled={action.isPending}
            onChange={async (e) => {
              resetInput();
              const version = inputVersion.current;
              const f = e.target.files?.[0];
              if (!f) return;
              try {
                if (f.size > 1024 ** 3)
                  throw new Error("The CSV limit is 1 GiB.");
                const text = await f.slice(0, 256 * 1024).text();
                if (version === inputVersion.current) loadCSV(f, text);
              } catch (err) {
                if (version === inputVersion.current) setError(err);
              }
            }}
          />
        </Field>
      )}
      {!!headers.length && (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {headers.map((h) => (
            <Field key={h} label={h || "(blank header)"}>
              <SelectField
                value={String(mapping[h])}
                onValueChange={(value) => {
                  resetPreview();
                  setMapping({ ...mapping, [h]: value });
                }}
                options={[
                  { value: "", label: "Ignore" },
                  { value: "email", label: "Email" },
                  { value: "name", label: "Name" },
                  {
                    value: "status",
                    label: "Status (includes opt-outs)",
                  },
                  { value: "fields", label: "Custom fields JSON" },
                  {
                    value: "gdpr_consent_at",
                    label: "Consent timestamp (milliseconds)",
                  },
                  ...fields.map((f) => ({
                    value: `fields.${f.key}`,
                    label: `${f.label} (${f.type})`,
                  })),
                ]}
              />
            </Field>
          ))}
        </div>
      )}
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Destination lists</legend>
        <div className="flex flex-wrap gap-4">
          {lists.map((l) => (
            <label className="text-sm flex gap-2 items-center" key={l.id}>
              <Checkbox
                checked={listIds.includes(l.id)}
                onCheckedChange={(checked) => {
                  resetPreview();
                  setListIds(
                    checked === true
                      ? [...listIds, l.id]
                      : listIds.filter((id) => id !== l.id),
                  );
                }}
              />
              {l.name}
            </label>
          ))}
        </div>
      </fieldset>
      <ErrorMessage error={error || action.error} />
      <Button
        variant="outline"
        disabled={
          !file ||
          action.isPending ||
          Object.values(mapping).filter((v) => v === "email").length !== 1
        }
        onClick={() => run(true)}
      >
        {action.isPending ? "Processing CSV…" : "Preview import"}
      </Button>
      {preview && (
        <div className="space-y-3">
          <p role="status" className="text-sm">
            {preview.rows.toLocaleString()} rows,{" "}
            {(preview.rows - preview.invalid).toLocaleString()} valid,{" "}
            {preview.invalid.toLocaleString()} invalid. No rows have been
            written.
          </p>
          {preview.errors?.map((s, i) => (
            <p key={i} className="text-sm text-destructive">
              {s}
            </p>
          ))}
          <div className="table-card overflow-x-auto rounded-lg border">
            <table className={tableClass}>
              <thead>
                <tr>
                  <th>Email</th>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Fields</th>
                </tr>
              </thead>
              <tbody>
                {preview.preview?.map((v, i) => (
                  <tr key={i}>
                    <td>{v.email}</td>
                    <td>{v.name}</td>
                    <td>{v.status || "active"}</td>
                    <td>
                      <code className="text-xs">
                        {JSON.stringify(v.fields)}
                      </code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="text-xs text-muted-foreground">
            Showing up to 20 valid rows. Double opt-in memberships remain
            unconfirmed; this import does not send confirmation emails.
          </p>
          <Button
            disabled={action.isPending || preview.rows === preview.invalid}
            onClick={() => setConfirm(true)}
          >
            Import valid rows
          </Button>
        </div>
      )}
      {result && (
        <StatusMessage>
          Imported {result.imported.toLocaleString()} rows. Skipped{" "}
          {result.invalid.toLocaleString()} invalid rows.
        </StatusMessage>
      )}
      <Dialog open={confirm} onOpenChange={setConfirm}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Commit CSV import?</DialogTitle>
          </DialogHeader>
          <p className="text-sm">
            Import{" "}
            {(preview ? preview.rows - preview.invalid : 0).toLocaleString()}{" "}
            valid rows from {file?.name} into{" "}
            {listIds.length
              ? `${listIds.length} selected lists`
              : "the brand without list memberships"}
            . Invalid rows will be skipped. Commits happen in batches.
          </p>
          <ErrorMessage error={action.error} />
          <DialogFooter>
            <Button
              variant="outline"
              disabled={action.isPending}
              onClick={() => setConfirm(false)}
            >
              Cancel
            </Button>
            <Button disabled={action.isPending} onClick={() => run(false)}>
              {action.isPending ? "Importing…" : "Confirm import"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

export function ExportSubscribers({
  brandId,
  lists,
}: {
  brandId: number;
  lists: SubscriberList[];
}) {
  const [list, setList] = useState(0);
  const [status, setStatus] = useState("");
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  return (
    <section className="rounded-xl border bg-card p-6 shadow-sm space-y-4">
      <h2 className="section-heading text-lg font-semibold">
        Export subscribers
      </h2>
      <p className="text-sm text-muted-foreground">
        Download emails, names, statuses, custom fields and consent dates. A
        list export includes membership opt-out status. Filter by status to
        download only the subscribers you need.
      </p>
      <div className="flex flex-wrap gap-3">
        <SelectField
          aria-label="Export list"
          className={" max-w-xs"}
          value={String(list)}
          onValueChange={(value) => setList(Number(value))}
          options={[
            { value: "0", label: "All brand subscribers" },
            ...lists.map((l) => ({ value: String(l.id), label: l.name })),
          ]}
        />
        <SelectField
          aria-label="Export status"
          className="max-w-xs"
          value={status}
          onValueChange={setStatus}
          options={[
            { value: "", label: "All statuses" },
            { value: "active", label: "Active (delivery)" },
            { value: "subscribed", label: "Subscribed" },
            { value: "unsubscribed", label: "Unsubscribed" },
            { value: "unconfirmed", label: "Unconfirmed" },
            { value: "bounced", label: "Bounced" },
            { value: "complained", label: "Complained" },
            { value: "blocklisted", label: "Blocklisted" },
          ]}
        />
        <Button
          variant="outline"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError(undefined);
            try {
              const params = new URLSearchParams();
              if (list) params.set("list_id", String(list));
              if (status) params.set("status", status);
              await download(
                `/subscribers/export${params.size ? `?${params}` : ""}`,
                brandId,
                "subscribers.csv",
              );
            } catch (e) {
              setError(e);
            } finally {
              setBusy(false);
            }
          }}
        >
          {busy ? "Preparing download…" : "Download CSV"}
        </Button>
      </div>
      <p className="text-sm text-muted-foreground">
        Unsubscribed includes brand-wide opt-outs and opt-outs from
        {list ? " the selected list" : " any list"}. Subscribed and unconfirmed
        exclude bounced, complained, blocklisted and brand-wide opted-out profiles.
      </p>
      <ErrorMessage error={error} />
    </section>
  );
}

export function MassOperations({
  brandId,
  lists,
}: {
  brandId: number;
  lists: SubscriberList[];
}) {
  const [text, setText] = useState("");
  const [emails, setEmails] = useState<string[] | null>(null);
  const [error, setError] = useState<unknown>();
  const [mode, setMode] = useState("unsubscribe");
  const [list, setList] = useState(0);
  const [confirmed, setConfirmed] = useState(false);
  const [result, setResult] = useState<number>();
  const action = useSubscriberAction<{ affected: number }>(brandId);
  const reset = () => {
    setEmails(null);
    setConfirmed(false);
    setResult(undefined);
    setError(undefined);
    action.reset();
  };
  const preview = () => {
    reset();
    const values = [
      ...new Set(
        text
          .split(/[\s,;]+/)
          .filter(Boolean)
          .map((e) => e.trim().toLowerCase()),
      ),
    ];
    if (!values.length || values.length > 10000) {
      setError(
        new Error("Provide between 1 and 10,000 distinct email addresses."),
      );
      return;
    }
    if (values.some((e) => !/^[^\s@]+@[^\s@]+$/.test(e))) {
      setError(new Error("Remove invalid email addresses before continuing."));
      return;
    }
    setEmails(values);
  };
  return (
    <section className="rounded-xl border bg-card p-6 shadow-sm space-y-4">
      <h2 className="section-heading text-lg font-semibold">
        Mass unsubscribe or GDPR erase
      </h2>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Action">
          <SelectField
            value={mode}
            onValueChange={(value) => {
              reset();
              setMode(value);
            }}
            options={[
              { value: "unsubscribe", label: "Unsubscribe" },
              { value: "erase", label: "GDPR erase" },
            ]}
          />
        </Field>
        {mode === "unsubscribe" && (
          <Field label="Scope">
            <SelectField
              value={String(list)}
              onValueChange={(value) => {
                reset();
                setList(Number(value));
              }}
              options={[
                { value: "0", label: "All lists in this brand" },
                ...lists.map((l) => ({ value: String(l.id), label: l.name })),
              ]}
            />
          </Field>
        )}
      </div>
      <Field label="Email addresses (one per line)">
        <Textarea
          rows={6}
          value={text}
          onChange={(e) => {
            reset();
            setText(e.target.value);
          }}
        />
      </Field>
      <Field label="Or choose a CSV with an email header">
        <Input
          type="file"
          accept=".csv,text/csv"
          onChange={async (e) => {
            reset();
            const f = e.target.files?.[0];
            if (!f) return;
            try {
              if (f.size > 10 * 1024 * 1024)
                throw new Error("Mass action CSVs must be at most 10 MiB.");
              const rows = parseCSV(await f.text(), 10002);
              if (rows.length > 10001)
                throw new Error("Mass actions support up to 10,000 rows.");
              const col =
                rows[0]?.findIndex(
                  (h) =>
                    h
                      .replace(/^\uFEFF/, "")
                      .trim()
                      .toLowerCase() === "email",
                ) ?? -1;
              if (col < 0) throw new Error("CSV requires an email header.");
              const data = rows.slice(1).filter((r) => r.some(Boolean));
              if (data.some((r) => !r[col]?.trim()))
                throw new Error(
                  "Every nonempty CSV row must contain an email address.",
                );
              setText(data.map((r) => r[col]).join("\n"));
            } catch (err) {
              setError(err);
            }
          }}
        />
      </Field>
      <ErrorMessage error={error || action.error} />
      <Button variant="outline" onClick={preview}>
        Preview addresses
      </Button>
      {emails && (
        <div className="space-y-3 rounded-lg bg-muted p-4">
          <p className="text-sm">
            {emails.length.toLocaleString()} unique addresses submitted for{" "}
            {mode === "erase"
              ? "permanent erasure of subscriber profiles, memberships and event history in this brand"
              : list
                ? `unsubscribe from ${lists.find((l) => l.id === list)?.name}`
                : "unsubscribe from all lists in this brand"}
            . This is an input count. Unknown addresses are skipped and the
            server reports the affected count after completion.
          </p>
          <pre className="max-h-40 overflow-auto text-xs">
            {emails.slice(0, 50).join("\n")}
            {emails.length > 50 ? "\n…" : ""}
          </pre>
          <label className="flex gap-2 text-sm items-center">
            <Checkbox
              checked={confirmed}
              onCheckedChange={(checked) => setConfirmed(checked === true)}
            />
            I have reviewed the addresses and scope.
          </label>
          <Button
            variant="destructive"
            disabled={!confirmed || action.isPending}
            onClick={async () => {
              try {
                const r = await action.mutateAsync({
                  path: `/subscribers/mass-${mode}`,
                  body: { emails, list_id: mode === "erase" ? 0 : list },
                });
                setResult(r.affected);
                setEmails(null);
                setConfirmed(false);
              } catch {
                setEmails(null);
                setConfirmed(false);
                setError(
                  new Error(
                    "The operation stopped. Some addresses may already have been processed. Refresh subscribers and preview again before retrying.",
                  ),
                );
              }
            }}
          >
            {action.isPending
              ? "Processing…"
              : mode === "erase"
                ? "Permanently erase subscribers"
                : "Unsubscribe subscribers"}
          </Button>
        </div>
      )}
      {result !== undefined && (
        <StatusMessage>
          Server processed {result.toLocaleString()} matching subscribers.
        </StatusMessage>
      )}
    </section>
  );
}

export function ListForm({
  brandId,
  list,
}: {
  brandId: number;
  list: SubscriberList;
}) {
  const form = useSubscribersQuery<{ hosted_url: string; html: string }>(
    brandId,
    `form-${list.id}`,
    `/lists/${list.id}/form`,
  );
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<unknown>();
  return (
    <div className="space-y-3">
      <ErrorMessage error={form.error} />
      {form.isPending ? (
        <p role="status">Loading form…</p>
      ) : (
        form.data && (
          <>
            <a
              className="block text-sm underline break-all"
              href={form.data.hosted_url}
              target="_blank"
              rel="noreferrer"
            >
              Open hosted subscribe form
            </a>
            <Field label="Embed HTML">
              <Textarea
                aria-label="Embed HTML"
                className="font-mono text-xs"
                readOnly
                rows={4}
                value={form.data.html}
              />
            </Field>
            <Button
              size="sm"
              onClick={async () => {
                setCopied(false);
                setCopyError(undefined);
                try {
                  await navigator.clipboard.writeText(form.data!.html);
                  setCopied(true);
                } catch {
                  setCopyError(new Error(
                    "Copy failed. Select and copy the HTML above.",
                  ));
                }
              }}
            >
              Copy embed HTML
            </Button>
            {copied && (
              <StatusMessage>Embed HTML copied to clipboard.</StatusMessage>
            )}
            <ErrorMessage error={copyError} />
          </>
        )
      )}
    </div>
  );
}

export function Housekeeping({ brandId }: { brandId: number }) {
  const { formatDateTime } = useDateTime();
  const [days, setDays] = useState(30);
  const [campaigns, setCampaigns] = useState(4);
  const [confirm, setConfirm] = useState(false);
  const [result, setResult] = useState("");
  const action = useSubscriberAction<{ removed?: number }>(brandId);
  const cutoff = Date.now() - days * 86400000;
  useEffect(() => {
    setResult("");
    setConfirm(false);
  }, [days]);
  return (
    <section className="rounded-xl border bg-card p-6 shadow-sm space-y-4">
      <h2 className="section-heading text-lg font-semibold">Housekeeping</h2>
      <Field label="Remove unconfirmed memberships older than (days)">
        <Input
          type="number"
          min="1"
          step="1"
          value={days}
          onChange={(e) => setDays(Number(e.target.value))}
        />
      </Field>
      <p className="text-sm text-muted-foreground">
        Removes stale double opt-in memberships and their confirmation tokens.
        Subscriber profiles and subscribed memberships remain.
      </p>
      <Button
        variant="outline"
        disabled={!Number.isInteger(days) || days < 1 || action.isPending}
        onClick={() => {
          action.reset();
          setConfirm(true);
        }}
      >
        Review cleanup
      </Button>
      <div className="border-t pt-4 space-y-3">
        <Field label="Inactive for the last N sent campaigns">
          <Input
            type="number"
            min="1"
            step="1"
            value={campaigns}
            onChange={(e) => setCampaigns(Number(e.target.value))}
          />
        </Field>
        <p className="text-sm text-muted-foreground">
          Creates a saved segment for subscribers with no recorded opens or
          clicks in the last N sent campaigns.
        </p>
        <Button
          variant="outline"
          disabled={
            !Number.isInteger(campaigns) || campaigns < 1 || action.isPending
          }
          onClick={async () => {
            try {
              await action.mutateAsync({
                path: "/segments",
                body: {
                  name: `Inactive for ${campaigns} campaigns`,
                  rules: { field: "inactive", value: campaigns },
                },
              });
              setResult(
                "Inactive segment created. Review it on the Segments page.",
              );
            } catch {
              /* displayed */
            }
          }}
        >
          Create inactive segment
        </Button>
      </div>
      <ErrorMessage error={action.error} />
      <StatusMessage>{result}</StatusMessage>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Remove stale unconfirmed memberships?"
        description={
          <>
            Remove unconfirmed memberships created before{" "}
            {formatDateTime(cutoff)} in this brand. This cannot be undone.
          </>
        }
        error={action.error}
        pending={action.isPending}
        pendingLabel="Removing…"
        confirmLabel="Remove memberships"
        onConfirm={async () => {
          try {
            const v = await action.mutateAsync({
              path: "/housekeeping/unconfirmed",
              body: { older_than: cutoff },
            });
            setResult(
              `Removed ${v.removed?.toLocaleString()} unconfirmed memberships.`,
            );
            setConfirm(false);
          } catch {
            /* displayed */
          }
        }}
      />
    </section>
  );
}
