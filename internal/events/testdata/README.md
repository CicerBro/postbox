# SNS certificate regression fixtures

These public certificates reproduce the missing-intermediate failure observed on
2026-10-11. Tests use a fixed date and mocked HTTP responses, without network access.

- `sns-signing.pem`: downloaded from https://sns.eu-central-1.amazonaws.com/SimpleNotificationService-1e59c4574facfe41babdb2d652f8ebef.pem
- `amazon-rsa-2048-m04.der`: downloaded from https://crt.r2m04.amazontrust.com/r2m04.cer
- `amazon-root-ca-1.pem`: downloaded from https://www.amazontrust.com/repository/AmazonRootCA1.pem

Amazon Trust Services publishes its certificates and licensing information at
https://www.amazontrust.com/repository/. The certificate files are unmodified.
