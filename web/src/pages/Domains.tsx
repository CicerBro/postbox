import { Globe2, ExternalLink } from "lucide-react";
import { Navigate } from "react-router-dom";
import { Section } from "../components/settings/Common";
import { SESDomain } from "../components/settings/SESDomain";
export function DomainsPage({ brandId }: { brandId: number }) {
  return (
    <Navigate
      replace
      to={brandId ? `/brands/${brandId}/settings/sending` : "/account/security"}
    />
  );
}
export function DomainsSettings({ brandId }: { brandId: number }) {
  return (
    <div className="space-y-6">
      {brandId ? (
        <>
          <SESDomain key={`ses-${brandId}`} brandId={brandId} />
          <Section
            title="Additional sending setup"
            description="Complete your authentication, event handling and tracking setup before sending."
          >
            <ol className="list-decimal space-y-4 pl-5 text-sm marker:font-semibold marker:text-foreground">
              <li>
                Use the domain verification section above to create your SES
                identity and publish its DKIM records at your DNS provider.
              </li>
              <li>
                Configure a custom MAIL FROM domain above and publish its MX and
                SPF records. Publish a DMARC policy for the sender domain.
              </li>
              <li>
                Select Set up SES and SNS in Amazon SES settings to create the
                event resources and subscribe this deployment. Refresh event
                status to confirm delivery is ready.
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
              Postbox displays the verification status reported by AWS. Publish
              DNS records at your DNS provider, then use Refresh status to check
              whether AWS has verified them.
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
