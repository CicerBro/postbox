-- Match the exposed sort expressions. Existing brand/email, brand/status/id,
-- and brand/created indexes already include the INTEGER PRIMARY KEY rowid
-- as their final ordering key, and can be read in either direction.
CREATE INDEX subscribers_brand_name_id ON subscribers(brand_id,name,id);
CREATE INDEX subscribers_brand_consent_id ON subscribers(brand_id,coalesce(gdpr_consent_at,0),id);
