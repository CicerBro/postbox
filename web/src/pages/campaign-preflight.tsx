import { CheckCircle2, CircleAlert, Info } from "lucide-react";
import { Badge } from "../components/ui/badge";
import type { Checklist } from "./campaign-checklist";
import { bytes, number } from "./campaign-shared";

type Issue = Checklist["checks"]["errors"][number];

function Issues({ issues }: { issues: Issue[] }) {
  return (
    <ul className="space-y-2 text-sm leading-relaxed">
      {issues.map((issue, index) => (
        <li key={index} className="break-words">
          {issue.message}
          {issue.tag && (
            <code className="ml-2 rounded bg-background/60 px-1.5 py-0.5 text-xs">
              {issue.tag}
            </code>
          )}
        </li>
      ))}
    </ul>
  );
}

export function Preflight({ data }: { data: Checklist }) {
  const c = data.checks;
  const links = c.links ?? [];
  const failedLinks = links.filter((link) =>
    ["broken", "blocked"].includes(link.status),
  );
  const reviewLinks = links.filter(
    (link) => link.status === "warning" || link.personalized,
  );
  // Link issues are summarized here and retain their full messages in the details.
  // Keep any issue without a matching link result visible in the main summary.
  const errors = (c.errors ?? []).filter(
    (issue) =>
      issue.code !== "link" ||
      !failedLinks.some(
        (link) => issue.message === `${link.url}: ${link.message}`,
      ),
  );
  const warnings = (c.warnings ?? []).filter(
    (issue) =>
      !["link", "personalized_link"].includes(issue.code) ||
      !links.some((link) => issue.message.startsWith(`${link.url}: `)),
  );
  const hasNotes =
    warnings.length > 0 || reviewLinks.length > 0 || failedLinks.length > 0;
  const unavailable = (c.errors ?? []).some((issue) =>
    ["template", "merge_tag", "audience"].includes(issue.code),
  );
  const quotaUnavailable =
    unavailable ||
    (c.errors ?? []).some((issue) => issue.code === "account_quota");
  const Icon = c.ready ? CheckCircle2 : CircleAlert;
  const duration =
    quotaUnavailable || (data.recipients > 0 && data.duration_seconds <= 0)
      ? "Unavailable"
      : data.recipients === 0
        ? "No recipients"
        : data.duration_seconds < 60
          ? "Under a minute"
          : `${number(Math.ceil(data.duration_seconds / 60))} minutes`;
  const allForbidden =
    failedLinks.length > 0 &&
    failedLinks.every((link) => link.http_status === 403);
  const uploadSize =
    data.upload_bytes >= 1024 ** 3
      ? `${(data.upload_bytes / 1024 ** 3).toFixed(1)} GB`
      : bytes(data.upload_bytes);

  return (
    <section
      aria-label="Preflight results"
      className="overflow-hidden rounded-xl border bg-background"
    >
      <div className="flex items-start gap-3 border-b bg-muted/30 p-5 sm:p-6">
        <Icon
          size={22}
          aria-hidden="true"
          className={`mt-1 shrink-0 ${c.ready ? "text-green-600 dark:text-green-400" : "text-destructive"}`}
        />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-3">
            <h3 className="text-lg font-semibold tracking-tight">
              {c.ready ? "Ready to send" : "Review before sending"}
            </h3>
            <Badge
              variant={
                c.ready ? (hasNotes ? "warning" : "success") : "destructive"
              }
            >
              {c.ready
                ? hasNotes
                  ? "Review notes"
                  : "Checks passed"
                : "Sending blocked"}
            </Badge>
          </div>
          <p className="mt-1 max-w-2xl text-sm leading-relaxed text-muted-foreground">
            {c.ready
              ? hasNotes
                ? "No blocking issues found. Review the notes below before sending."
                : "No blocking issues found. Your saved campaign is ready to send."
              : "Resolve the issues below, then run preflight again to check your campaign."}
          </p>
        </div>
      </div>

      <div className="space-y-6 p-5 sm:p-6">
        {errors.length > 0 && (
          <div className="space-y-4 rounded-lg border border-red-200 bg-red-50 p-4 text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200">
            <h4 className="font-semibold">What needs attention</h4>
            <Issues issues={errors} />
          </div>
        )}

        {hasNotes && (
          <div className="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
            <Info size={18} className="mt-0.5 shrink-0" aria-hidden="true" />
            <div className="min-w-0 space-y-2">
              <h4 className="text-sm font-semibold">Worth reviewing</h4>
              {warnings.length > 0 && <Issues issues={warnings} />}
              {failedLinks.length > 0 && (
                <div>
                  <p className="text-sm font-semibold">
                    {number(failedLinks.length)}{" "}
                    {failedLinks.length === 1 ? "link could" : "links could"} not
                    be verified
                  </p>
                  <p className="mt-1 max-w-3xl text-sm leading-relaxed">
                    {allForbidden
                      ? "The website refused the automated check (HTTP 403). Cloudflare or other bot protection may block these requests even when the pages work in a browser. Open the links to verify the destinations."
                      : "Some destinations failed the automated check. Review the link results below and correct any invalid URLs or inaccessible pages."}
                    {" "}Link checks are advisory and do not block sending or scheduling.
                  </p>
                </div>
              )}
              {reviewLinks.length > 0 && (
                <p className="text-sm leading-relaxed">
                  {number(reviewLinks.length)}{" "}
                  {reviewLinks.length === 1 ? "link needs" : "links need"} a
                  manual review. Redirects and personalized destinations should
                  be checked in a real recipient preview. See the link results
                  below.
                </p>
              )}
            </div>
          </div>
        )}

        {unavailable ? (
          <p className="text-sm text-muted-foreground">
            Audience and delivery estimates will be available after the campaign
            issues are resolved.
          </p>
        ) : (
          <>
            <dl className="grid gap-6 sm:grid-cols-2">
              <div>
                <dt className="text-sm font-medium text-muted-foreground">
                  Eligible recipients
                </dt>
                <dd className="mt-1 text-3xl font-semibold tracking-tight tabular-nums">
                  {number(data.recipients)}
                </dd>
                <dd className="mt-2 max-w-md text-sm leading-relaxed text-muted-foreground">
                  {c.skipped > 0
                    ? `${number(c.skipped)} contacts excluded: ${number(c.suppressed)} suppressed and ${number(c.inactive)} inactive.`
                    : "No suppressed or inactive contacts in this audience."}
                </dd>
              </div>
              <div>
                <dt className="text-sm font-medium text-muted-foreground">
                  Estimated sending time
                </dt>
                <dd className="mt-1 text-3xl font-semibold tracking-tight tabular-nums">
                  {duration}
                </dd>
                <dd className="mt-2 text-sm leading-relaxed text-muted-foreground">
                  {quotaUnavailable
                    ? "The sending rate could not be checked."
                    : "Based on the current SES sending rate."}
                </dd>
              </div>
            </dl>

            <div className="grid gap-6 border-t pt-5 sm:grid-cols-2">
              <div className="space-y-2">
                <h4 className="text-sm font-semibold">Sending capacity</h4>
                <Badge
                  variant={
                    quotaUnavailable
                      ? "secondary"
                      : data.fits_daily_quota
                        ? "success"
                        : "destructive"
                  }
                >
                  {quotaUnavailable
                    ? "Quota unavailable"
                    : data.fits_daily_quota
                      ? "Within SES daily quota"
                      : "Exceeds SES daily quota"}
                </Badge>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {quotaUnavailable
                    ? "Resolve the account issue above to check available capacity."
                    : `${number(c.remaining_daily_quota)} sends remain in the SES daily quota for ${number(data.recipients)} recipients.`}
                </p>
                {c.user_remaining_daily_quota !== undefined && (
                  <p className="text-sm leading-relaxed text-muted-foreground">
                    Campaign owner’s daily budget:{" "}
                    {number(c.user_remaining_daily_quota)} sends remaining.
                  </p>
                )}
              </div>
              <div className="space-y-2">
                <h4 className="text-sm font-semibold">Email size</h4>
                <Badge
                  variant={
                    c.html_bytes <= 0
                      ? "secondary"
                      : c.html_clipping
                        ? "warning"
                        : "success"
                  }
                >
                  {c.html_bytes <= 0
                    ? "Size unavailable"
                    : c.html_clipping
                      ? "Gmail may clip this email"
                      : "Below Gmail’s clipping threshold"}
                </Badge>
                <p className="text-sm leading-relaxed text-muted-foreground">
                  {c.html_bytes > 0
                    ? `Rendered HTML is ${bytes(c.html_bytes)} of Gmail’s 102 KB threshold.`
                    : "The rendered HTML size could not be estimated."}
                </p>
                {data.message_bytes > 0 && (
                  <p className="text-sm leading-relaxed text-muted-foreground">
                    {bytes(data.message_bytes)} per email, including headers.
                    About {uploadSize} to upload for the full audience.
                  </p>
                )}
              </div>
            </div>
          </>
        )}

        {links.length > 0 && (
          <details className="group rounded-lg border">
            <summary className="cursor-pointer rounded-lg px-4 py-3 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              Link results{" "}
              <span className="ml-1 font-normal text-muted-foreground">
                ({number(links.length)} checked
                {failedLinks.length > 0
                  ? `, ${number(failedLinks.length)} unverified`
                  : ""}
                )
              </span>
            </summary>
            <ul className="divide-y border-t">
              {links.map((link, index) => (
                <li
                  key={index}
                  className="flex flex-col gap-2 px-4 py-3 text-sm sm:flex-row sm:items-start sm:justify-between sm:gap-4"
                >
                  <div className="min-w-0 space-y-1">
                    <p className="break-all font-medium">
                      {!link.personalized && /^https?:\/\//i.test(link.url) ? (
                        <a
                          href={link.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="underline decoration-border underline-offset-4 hover:decoration-current focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          {link.url}
                        </a>
                      ) : (
                        link.url
                      )}
                    </p>
                    {(link.message || link.personalized) && (
                      <p className="break-words leading-relaxed text-muted-foreground">
                        {link.message}
                        {link.personalized
                          ? " Checked with sample values; verify in a real recipient preview."
                          : ""}
                      </p>
                    )}
                  </div>
                  <Badge
                    className="w-fit shrink-0"
                    variant={link.status === "ok" ? "success" : "warning"}
                  >
                    {link.status === "ok"
                      ? "Passed"
                      : ["broken", "blocked"].includes(link.status)
                        ? "Unverified"
                        : "Review"}
                    {link.http_status ? ` · HTTP ${link.http_status}` : ""}
                  </Badge>
                </li>
              ))}
            </ul>
          </details>
        )}
        <p className="border-t pt-4 text-xs leading-relaxed text-muted-foreground">
          Estimates use sample merge values. Audience, quota and sending time
          can change before delivery. Running preflight does not send emails or
          reserve quota.
        </p>
      </div>
    </section>
  );
}
