package service

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The signing-key derivation vector published in the AWS documentation
// ("Examples of how to derive a signing key for Signature Version 4"). This is
// the load-bearing check: the chain is four HMACs and a single wrong input
// produces a signature that fails only as an opaque SignatureDoesNotMatch.
func TestDeriveSigningKeyMatchesAWSVector(t *testing.T) {
	got := hex.EncodeToString(deriveSigningKey(
		"wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		"20120215", "us-east-1", "iam",
	))
	const want = "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1a9e090d414db404d"
	if got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

func TestSHA256HexOfEmptyStringMatchesTheConstant(t *testing.T) {
	// emptyPayloadHash is hard-coded into every canonical request, so it has to
	// actually be SHA256("").
	if got := sha256Hex(""); got != emptyPayloadHash {
		t.Fatalf("SHA256(\"\") = %s, but emptyPayloadHash is %s", got, emptyPayloadHash)
	}
}

// SigV4's encoding differs from url.QueryEscape in exactly the ways that break
// signatures silently, so each one is pinned.
func TestAWSURIEncode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"simple", "simple"},
		{"with space", "with%20space"}, // not "+"
		{"tilde~", "tilde~"},           // unreserved, must not be escaped
		{"a-b_c.d~e", "a-b_c.d~e"},
		{"plus+sign", "plus%2Bsign"},
		{"equals=", "equals%3D"},
		{"amp&", "amp%26"},
		{"utf8-é", "utf8-%C3%A9"},
		{"colon:", "colon%3A"},
		{"star*", "star%2A"},
	}
	for _, c := range cases {
		if got := awsURIEncode(c.in, true); got != c.want {
			t.Errorf("awsURIEncode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A path keeps its separators.
	if got := awsURIEncode("/a/b", false); got != "/a/b" {
		t.Errorf("path encoding = %q, want /a/b", got)
	}
	if got := awsURIEncode("/a/b", true); got != "%2Fa%2Fb" {
		t.Errorf("query encoding of a slash = %q, want %%2Fa%%2Fb", got)
	}
}

func TestCanonicalQuerySortsAndEncodes(t *testing.T) {
	values := url.Values{
		"Version": {"2016-11-15"},
		"Action":  {"DescribeInstances"},
		"Filter":  {"b value", "a value"},
	}
	// Names sorted, then values within a repeated name, spaces as %20.
	want := "Action=DescribeInstances&Filter=a%20value&Filter=b%20value&Version=2016-11-15"
	if got := canonicalQuery(values); got != want {
		t.Fatalf("canonicalQuery =\n%s\nwant\n%s", got, want)
	}
	if got := canonicalQuery(url.Values{}); got != "" {
		t.Errorf("empty query = %q, want empty string", got)
	}
}

func TestCanonicalHeadersLowercasesSortsAndTrims(t *testing.T) {
	block, signed := canonicalHeaders(map[string]string{
		"X-Amz-Date": "20150830T123600Z",
		"Host":       "  example.amazonaws.com  ",
	})
	if signed != "host;x-amz-date" {
		t.Fatalf("signed headers = %q, want host;x-amz-date", signed)
	}
	want := "host:example.amazonaws.com\nx-amz-date:20150830T123600Z\n"
	if block != want {
		t.Fatalf("header block = %q, want %q", block, want)
	}
}

func TestSignAWSRequestSetsAuthorizationAndDateHeaders(t *testing.T) {
	req, err := http.NewRequest("GET", "https://ec2.us-east-1.amazonaws.com/?Action=DescribeInstances&Version=2016-11-15", nil)
	if err != nil {
		t.Fatal(err)
	}
	creds := awsCredentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Region:          "us-east-1",
	}
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	signAWSRequest(req, creds, "ec2", at)

	if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
		t.Errorf("X-Amz-Date = %q", got)
	}
	auth := req.Header.Get("Authorization")
	for _, want := range []string{
		"AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/ec2/aws4_request",
		"SignedHeaders=host;x-amz-date",
		"Signature=",
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("Authorization %q is missing %q", auth, want)
		}
	}
	// A session token must be signed, not just sent, or AWS rejects it.
	if req.Header.Get("X-Amz-Security-Token") != "" {
		t.Error("no session token was supplied, so none should be sent")
	}
}

func TestSignAWSRequestSignsSessionToken(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://ec2.us-east-1.amazonaws.com/?Action=DescribeInstances", nil)
	signAWSRequest(req, awsCredentials{
		AccessKeyID:     "ASIA",
		SecretAccessKey: "secret",
		SessionToken:    "token-value",
		Region:          "eu-west-1",
	}, "ec2", time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))

	if got := req.Header.Get("X-Amz-Security-Token"); got != "token-value" {
		t.Errorf("session token header = %q", got)
	}
	if auth := req.Header.Get("Authorization"); !strings.Contains(auth, "SignedHeaders=host;x-amz-date;x-amz-security-token") {
		t.Errorf("session token was not included in SignedHeaders: %q", auth)
	}
}

// Two requests differing only in a query value must produce different
// signatures — otherwise the canonical request is not actually feeding in.
func TestSignAWSRequestSignatureCoversTheQuery(t *testing.T) {
	creds := awsCredentials{AccessKeyID: "AKID", SecretAccessKey: "secret", Region: "us-east-1"}
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	a, _ := http.NewRequest("GET", "https://ec2.amazonaws.com/?Action=DescribeInstances", nil)
	b, _ := http.NewRequest("GET", "https://ec2.amazonaws.com/?Action=DescribeRegions", nil)
	signAWSRequest(a, creds, "ec2", at)
	signAWSRequest(b, creds, "ec2", at)

	if a.Header.Get("Authorization") == b.Header.Get("Authorization") {
		t.Fatal("different queries produced the same signature")
	}
}
