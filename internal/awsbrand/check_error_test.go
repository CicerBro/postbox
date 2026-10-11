package awsbrand

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/aws/smithy-go"
)

func TestConnectionCheckErrorRedaction(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timed out"},
		{&url.Error{Op: "Post", URL: "https://secret-value", Err: &net.DNSError{Err: "secret-value", Name: "secret-value"}}, "DNS lookup failed"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "connection was refused"},
		{&url.Error{Op: "Post", URL: "https://secret-value", Err: x509.UnknownAuthorityError{}}, "TLS certificate is not trusted"},
		{x509.HostnameError{Host: "secret-value"}, "does not match the endpoint hostname"},
		{x509.CertificateInvalidError{Reason: x509.Expired}, "TLS certificate is invalid"},
		{&smithy.GenericAPIError{Code: "ExpiredToken", Message: "secret-value"}, "session token has expired"},
		{&smithy.GenericAPIError{Code: "RequestExpired", Message: "secret-value"}, "server clock"},
		{&smithy.GenericAPIError{Code: "ThrottlingException", Message: "secret-value"}, "throttling"},
		{errors.New("secret-value"), "could not complete the request"},
	} {
		got := ConnectionCheckError(fmt.Errorf("wrapped: %w", awsCheckError("ses:GetAccount", "eu-west-1", tc.err))).Error()
		if !strings.Contains(got, tc.want) || strings.Contains(got, "secret-value") {
			t.Fatalf("unsafe or unhelpful error: %s", got)
		}
	}
	if got := ConnectionCheckError(errors.New("secret-value")).Error(); strings.Contains(got, "secret-value") {
		t.Fatal(got)
	}
}
