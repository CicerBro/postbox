import { Globe2, ExternalLink } from "lucide-react";
import { BrandIdentity } from "../components/settings/BrandIdentity";
import { Section } from "../components/settings/Common";
export function DomainsPage({ brandId }: { brandId: number }) {
  return (
    <div className="space-y-6">
      <div className="page-heading">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Domains</h1>
          <p className="page-subtitle">
            Configure your sending identity and branded tracking links.
          </p>
        </div>
        <Globe2
          size={24}
          className="text-muted-foreground"
          aria-hidden="true"
        />
      </div>
      {brandId ? (
        <>
          <BrandIdentity key={brandId} brandId={brandId} />
          <Section
            title="DNS and Amazon SES setup"
            description="Complete these steps in your AWS console and DNS provider before sending."
          >
            <ol className="list-decimal space-y-4 pl-5 text-sm marker:font-semibold marker:text-foreground">
              <li>
                In Amazon SES, create and verify the sender domain in the region
                used by Postbox. Publish the exact Easy DKIM records supplied by
                SES.
              </li>
              <li>
                Configure a custom MAIL FROM domain in SES and publish its
                supplied MX and SPF records. Publish a DMARC policy for the
                sender domain.
              </li>
              <li>
                Create the configuration set above in SES. Configure its event
                destination for delivery, bounce and complaint events through
                the deployment’s SNS or SQS ingestion.
              </li>
              <li>
                Point the tracking hostname to this Postbox deployment and serve
                it over HTTPS. The server’s trusted base URL must be configured
                for public links and passkeys.
              </li>
            </ol>
            <a
              href="https://console.aws.amazon.com/ses/"
              target="_blank"
              rel="noreferrer"
              className="button button-outline"
            >
              <ExternalLink size={15} aria-hidden="true" />
              Open Amazon SES
            </a>
            <p className="rounded-lg bg-muted p-3 text-sm text-muted-foreground">
              Verification is performed in the AWS console. Postbox has no SES
              identity-status endpoint, so this page does not report
              verification status or generate DNS records.
            </p>
          </Section>
        </>
      ) : (
        <div className="empty-panel">
          <Globe2 size={28} aria-hidden="true" />
          <h2>Select a brand</h2>
          <p className="page-subtitle">
            Choose a brand to configure its sender and tracking domains.
          </p>
        </div>
      )}
    </div>
  );
}
