-- Keep delivery-status audience searches bounded within a brand and ID cursor.
CREATE INDEX subscribers_brand_status_id ON subscribers(brand_id,status,id);
