package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4, hand-rolled.
//
// The AWS SDK is not available here (no new module dependencies), and SigV4 is
// a closed, fully specified algorithm, so this implements the subset the EC2
// Query API needs: GET, no payload, headers signed being host and x-amz-date
// (plus x-amz-security-token when the credentials are temporary).
//
// The spec is unforgiving about canonicalisation and every mistake surfaces
// only as an opaque SignatureDoesNotMatch from the service, so the pieces are
// separated out and tested against the vectors AWS publishes.

const (
	sigV4Algorithm = "AWS4-HMAC-SHA256"
	// emptyPayloadHash is SHA256("") — every request here is a bodyless GET.
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type awsCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	// SessionToken is set for temporary credentials (STS, instance roles). It
	// must be both sent and signed, or the request is rejected.
	SessionToken string
	Region       string
}

// hmacSHA256 is the primitive the whole derivation chain is built from.
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// deriveSigningKey runs the four-step HMAC chain that turns a long-lived
// secret into a key scoped to one date, region and service. The scoping is
// what makes a leaked signature useless outside its day and service.
func deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

// canonicalQuery re-encodes and sorts query parameters the way SigV4 requires:
// sorted by name, RFC 3986 encoded, with spaces as %20 rather than +.
func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		// Values for a repeated key sort among themselves.
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(awsURIEncode(k, true))
			b.WriteByte('=')
			b.WriteString(awsURIEncode(v, true))
		}
	}
	return b.String()
}

// awsURIEncode implements the encoding SigV4 specifies, which differs from
// url.QueryEscape in two ways that matter: space is %20 not +, and tilde is
// left alone. encodeSlash is false only when encoding a URI path.
func awsURIEncode(s string, encodeSlash bool) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(unreserved, c) >= 0:
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte('/')
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// canonicalHeaders renders the signed headers block and the accompanying
// signed-header list. Names are lowercased and values trimmed, per spec.
func canonicalHeaders(headers map[string]string) (block, signed string) {
	names := make([]string, 0, len(headers))
	lower := make(map[string]string, len(headers))
	for name, value := range headers {
		l := strings.ToLower(name)
		names = append(names, l)
		lower[l] = strings.TrimSpace(value)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(lower[name])
		b.WriteByte('\n')
	}
	return b.String(), strings.Join(names, ";")
}

// signAWSRequest adds the SigV4 Authorization header (and the x-amz-* headers
// it covers) to a bodyless GET request.
func signAWSRequest(req *http.Request, creds awsCredentials, service string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")

	headers := map[string]string{
		"host":       req.Host,
		"x-amz-date": amzDate,
	}
	if req.Host == "" {
		headers["host"] = req.URL.Host
	}
	if creds.SessionToken != "" {
		headers["x-amz-security-token"] = creds.SessionToken
	}

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	headerBlock, signedHeaders := canonicalHeaders(headers)

	canonicalRequest := strings.Join([]string{
		req.Method,
		path,
		canonicalQuery(req.URL.Query()),
		headerBlock,
		signedHeaders,
		emptyPayloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, creds.Region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		sigV4Algorithm,
		amzDate,
		scope,
		sha256Hex(canonicalRequest),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(
		deriveSigningKey(creds.SecretAccessKey, dateStamp, creds.Region, service),
		[]byte(stringToSign),
	))

	for name, value := range headers {
		if name != "host" { // net/http sets Host from the URL itself
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Authorization", strings.Join([]string{
		sigV4Algorithm + " Credential=" + creds.AccessKeyID + "/" + scope,
		"SignedHeaders=" + signedHeaders,
		"Signature=" + signature,
	}, ", "))
}
