import { BrandIdentity } from "../components/settings/BrandIdentity";
import { Section } from "../components/settings/Common";
export function DomainsPage({ brandId }: { brandId: number }) {
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">Domains</h1>
      {brandId ? (
        <>
          <BrandIdentity key={brandId} brandId={brandId} />
          <Section title="DNS and Amazon SES setup">
            <ol className="list-decimal space-y-3 pl-5 text-sm">
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
            <p className="text-sm text-muted-foreground">
              Verification is performed in the AWS console. Postbox has no SES
              identity-status endpoint, so this page does not report
              verification status or generate DNS records.
            </p>
          </Section>
        </>
      ) : (
        <p>Select a brand to configure domains.</p>
      )}
    </div>
  );
}
