package awsbrand

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/aws/smithy-go"
)

// checkError contains only locally authored text, never AWS response messages.
type checkError struct{ message string }

func (e *checkError) Error() string { return e.message }

func awsCheckError(operation, region string, err error) error {
	reason := "AWS could not complete the request. Check connectivity, region, and service availability."
	code := ""
	var api smithy.APIError
	var network net.Error
	var dns *net.DNSError
	var untrusted x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		reason = "The request timed out. Check connectivity and try again."
	case errors.As(err, &dns):
		reason = "DNS lookup failed for the AWS endpoint. Check DNS in the server or container and any AWS endpoint overrides."
	case errors.As(err, &untrusted):
		reason = "The AWS endpoint TLS certificate is not trusted. Check the server CA certificates and any HTTPS proxy."
	case errors.As(err, &hostname):
		reason = "The TLS certificate does not match the endpoint hostname. Check AWS endpoint overrides and any HTTPS proxy."
	case errors.As(err, &certificate):
		reason = "The endpoint TLS certificate is invalid. Check the server clock, CA certificates, and any HTTPS proxy."
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "The connection was refused. Check AWS endpoint overrides, proxy settings, and outbound HTTPS access."
	case errors.As(err, &api):
		code = api.ErrorCode()
		switch code {
		case "InvalidClientTokenId", "UnrecognizedClientException", "InvalidAccessKeyId":
			reason = "AWS rejected the access key or session token. Check that the key is active and the saved token is correct."
		case "SignatureDoesNotMatch", "InvalidSignatureException":
			reason = "AWS rejected the request signature. Check that the secret matches the access key, has no extra whitespace, and is an AWS secret rather than an SMTP password."
		case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation":
			reason = "AWS denied permission. Allow " + operation + " in the IAM policy and check any account or organization restrictions."
		case "ExpiredToken", "ExpiredTokenException":
			reason = "The AWS session token has expired. Replace the temporary credentials."
		case "RequestExpired", "RequestTimeTooSkewed":
			reason = "AWS rejected the request time. Check the server clock."
		case "Throttling", "ThrottlingException", "TooManyRequestsException":
			reason = "AWS is throttling requests. Wait and try again."
		default:
			code = ""
		}
	case errors.As(err, &network):
		if network.Timeout() {
			reason = "The network connection timed out. Check outbound HTTPS access and proxy settings."
		} else {
			reason = "AWS could not be reached. Check the server network, DNS, endpoint overrides, and proxy settings."
		}
	}
	if code != "" {
		reason = code + ": " + reason
	}
	return &checkError{message: fmt.Sprintf("AWS connection check failed at %s in %s. %s", operation, region, reason)}
}

// ConnectionCheckError exposes safe diagnostics and hides arbitrary internal errors.
func ConnectionCheckError(err error) error {
	var safe *checkError
	if errors.As(err, &safe) {
		return safe
	}
	return errors.New("AWS connection check failed. Check credentials, region, and SES permissions.")
}
