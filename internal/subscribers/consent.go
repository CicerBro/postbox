package subscribers

// MarketingEligibilitySQL is shared by selection and the final dispatch check.
// The subscriber table must use alias s. Confirmation messages intentionally use
// their own token and suppression checks because they request consent.
const MarketingEligibilitySQL = `s.status='active' AND s.marketing_unsubscribed_at IS NULL AND NOT EXISTS(SELECT 1 FROM suppressions p WHERE (p.brand_id IS NULL OR p.brand_id=s.brand_id) AND ((p.kind='email' AND p.value=s.email) OR (p.kind='domain' AND p.value=s.domain)))`
