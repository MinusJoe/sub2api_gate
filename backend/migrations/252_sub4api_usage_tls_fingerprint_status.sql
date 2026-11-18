-- NULL also covers historical usage for which the connection cannot be observed.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS tls_fingerprint_status text;
