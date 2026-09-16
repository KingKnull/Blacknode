package service

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/blacknode/blacknode/internal/db"
	"github.com/blacknode/blacknode/internal/store"
	_ "modernc.org/sqlite"
)

// stubTransport serves canned bodies keyed by a substring of the request URL,
// so discovery can be exercised end to end without network access.
type stubTransport struct {
	responses map[string]string
	status    int
	requests  []*http.Request
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, req)
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	for fragment, body := range s.responses {
		if strings.Contains(req.URL.String(), fragment) {
			return &http.Response{
				StatusCode: status,
				Status:     http.StatusText(status),
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}
	}
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Body:       io.NopCloser(strings.NewReader(`{"message":"no stub"}`)),
		Header:     make(http.Header),
	}, nil
}

func newCloudService(t *testing.T, transport http.RoundTripper) (*CloudImportService, *store.Hosts) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	hosts := store.NewHosts(conn)
	svc := NewCloudImportService(hosts)
	svc.client = &http.Client{Transport: transport}
	return svc, hosts
}

// A real DescribeInstances response, trimmed to the fields discovery reads.
const ec2Response = `<?xml version="1.0" encoding="UTF-8"?>
<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <reservationSet>
    <item>
      <instancesSet>
        <item>
          <instanceId>i-0abc123</instanceId>
          <instanceType>t3.medium</instanceType>
          <dnsName>ec2-1-2-3-4.compute-1.amazonaws.com</dnsName>
          <ipAddress>1.2.3.4</ipAddress>
          <privateIpAddress>10.0.0.5</privateIpAddress>
          <placement><availabilityZone>us-east-1a</availabilityZone></placement>
          <instanceState><name>running</name></instanceState>
          <platformDetails>Ubuntu Pro</platformDetails>
          <tagSet>
            <item><key>Name</key><value>web-1</value></item>
            <item><key>Env</key><value>prod</value></item>
            <item><key>aws:cloudformation:stack</key><value>ignored</value></item>
          </tagSet>
        </item>
        <item>
          <instanceId>i-0def456</instanceId>
          <instanceType>m5.large</instanceType>
          <privateIpAddress>10.0.0.6</privateIpAddress>
          <placement><availabilityZone>us-east-1b</availabilityZone></placement>
          <instanceState><name>running</name></instanceState>
          <platformDetails>Linux/UNIX</platformDetails>
          <tagSet/>
        </item>
      </instancesSet>
    </item>
  </reservationSet>
</DescribeInstancesResponse>`

func TestDiscoverAWSParsesInstances(t *testing.T) {
	svc, _ := newCloudService(t, &stubTransport{
		responses: map[string]string{"ec2.us-east-1.amazonaws.com": ec2Response},
	})

	found, err := svc.Discover(context.Background(), CloudCredentials{
		Provider: "aws", AccessKeyID: "AKID", SecretAccessKey: "secret", Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d hosts, want 2: %+v", len(found), found)
	}

	web := found[0]
	// The Name tag becomes the host name; the instance ID is only a fallback.
	if web.Name != "web-1" {
		t.Errorf("name = %q, want web-1", web.Name)
	}
	// Public DNS is preferred over the IP: it survives a stop/start.
	if web.Host != "ec2-1-2-3-4.compute-1.amazonaws.com" {
		t.Errorf("host = %q, want the public DNS name", web.Host)
	}
	if web.Username != "ubuntu" {
		t.Errorf("username = %q, want ubuntu for an Ubuntu image", web.Username)
	}
	if web.Platform != "ubuntu" {
		t.Errorf("platform = %q, want ubuntu", web.Platform)
	}
	if web.Region != "us-east-1a" {
		t.Errorf("region = %q", web.Region)
	}
	// aws:-prefixed tags are provider bookkeeping, not user labels.
	for _, tag := range web.Tags {
		if strings.HasPrefix(tag, "aws:") {
			t.Errorf("kept an aws: internal tag: %q", tag)
		}
	}
	if len(web.Tags) != 1 || web.Tags[0] != "env:prod" {
		t.Errorf("tags = %v, want [env:prod]", web.Tags)
	}

	// No public address at all, so it falls back to the private one.
	if found[1].Host != "10.0.0.6" {
		t.Errorf("second host = %q, want the private IP fallback", found[1].Host)
	}
	if found[1].Name != "i-0def456" {
		t.Errorf("untagged instance name = %q, want the instance ID", found[1].Name)
	}
	if found[1].Username != "ec2-user" {
		t.Errorf("Linux/UNIX username = %q, want ec2-user", found[1].Username)
	}
}

const doResponse = `{
  "droplets": [
    {"id": 1, "name": "app-1", "status": "active",
     "tags": ["web", "prod"],
     "region": {"slug": "nyc3"}, "size": {"slug": "s-2vcpu-4gb"},
     "image": {"distribution": "Ubuntu"},
     "networks": {"v4": [
       {"ip_address": "10.1.0.2", "type": "private"},
       {"ip_address": "203.0.113.5", "type": "public"}
     ]}},
    {"id": 2, "name": "off-1", "status": "off",
     "networks": {"v4": [{"ip_address": "203.0.113.6", "type": "public"}]}},
    {"id": 3, "name": "vpc-only", "status": "active",
     "image": {"distribution": "Debian"},
     "networks": {"v4": [{"ip_address": "10.1.0.9", "type": "private"}]}}
  ],
  "links": {}
}`

func TestDiscoverDigitalOceanSkipsInactiveAndPrefersPublicIP(t *testing.T) {
	svc, _ := newCloudService(t, &stubTransport{
		responses: map[string]string{"api.digitalocean.com": doResponse},
	})

	found, err := svc.Discover(context.Background(), CloudCredentials{
		Provider: "digitalocean", Token: "dop_v1_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The "off" droplet is not connectable and must not be offered.
	if len(found) != 2 {
		t.Fatalf("found %d droplets, want 2 (the off one skipped): %+v", len(found), found)
	}
	if found[0].Host != "203.0.113.5" {
		t.Errorf("host = %q, want the public IP over the private one", found[0].Host)
	}
	if found[0].Username != "root" {
		t.Errorf("username = %q, want root", found[0].Username)
	}
	if found[0].Platform != "ubuntu" {
		t.Errorf("platform = %q, want ubuntu", found[0].Platform)
	}
	// A VPC-only droplet is still importable — it is reachable via a bastion.
	if found[1].Host != "10.1.0.9" {
		t.Errorf("vpc-only host = %q, want the private IP", found[1].Host)
	}
}

func TestDiscoverDigitalOceanSendsBearerToken(t *testing.T) {
	transport := &stubTransport{responses: map[string]string{"api.digitalocean.com": doResponse}}
	svc, _ := newCloudService(t, transport)
	if _, err := svc.Discover(context.Background(), CloudCredentials{Provider: "digitalocean", Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	if got := transport.requests[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want Bearer tok", got)
	}
}

func TestDiscoverRejectsMissingCredentials(t *testing.T) {
	svc, _ := newCloudService(t, &stubTransport{})
	cases := []CloudCredentials{
		{Provider: "aws"},
		{Provider: "aws", AccessKeyID: "only-key"},
		{Provider: "digitalocean"},
		{Provider: "azure"},
		{Provider: "azure", TenantID: "t", ClientID: "c"},
		{Provider: "gcp"}, // unsupported
		{Provider: ""},
	}
	for _, creds := range cases {
		if _, err := svc.Discover(context.Background(), creds); err == nil {
			t.Errorf("accepted incomplete credentials: %+v", creds)
		}
	}
}

// A provider error body is what actually tells the user their key is wrong, so
// it has to survive into the returned error.
func TestDiscoverSurfacesProviderErrorBody(t *testing.T) {
	svc, _ := newCloudService(t, &stubTransport{
		status:    http.StatusForbidden,
		responses: map[string]string{"api.digitalocean.com": `{"id":"unauthorized","message":"Unable to authenticate you"}`},
	})
	_, err := svc.Discover(context.Background(), CloudCredentials{Provider: "digitalocean", Token: "bad"})
	if err == nil {
		t.Fatal("expected an error for a 403")
	}
	if !strings.Contains(err.Error(), "Unable to authenticate you") {
		t.Errorf("error %q does not include the provider's message", err)
	}
}

func TestImportSkipsHostsThatAlreadyExist(t *testing.T) {
	svc, hosts := newCloudService(t, &stubTransport{})
	if _, err := hosts.Create(store.Host{
		Name: "existing", Host: "203.0.113.5", Username: "ops", AuthMethod: "key",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Import(context.Background(), ImportRequest{
		Hosts: []DiscoveredHost{
			{Name: "new-1", Host: "203.0.113.10", Port: 22, Username: "ubuntu"},
			{Name: "dupe-by-address", Host: "203.0.113.5", Port: 22, Username: "ubuntu"},
			{Name: "existing", Host: "203.0.113.99", Port: 22, Username: "ubuntu"},
		},
		Group: "imported",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Errorf("imported = %d, want 1", result.Imported)
	}
	// One matched by address, one by name.
	if result.Skipped != 2 {
		t.Errorf("skipped = %d, want 2", result.Skipped)
	}
	if len(result.Errors) != 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}

	saved, err := hosts.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("host count = %d, want 2", len(saved))
	}
	for _, h := range saved {
		if h.Name == "new-1" {
			if h.Group != "imported" {
				t.Errorf("group = %q, want imported", h.Group)
			}
			if h.AuthMethod != "key" {
				t.Errorf("auth method = %q, want the key default", h.AuthMethod)
			}
		}
	}
}

func TestImportAppliesOverridesAndDefaults(t *testing.T) {
	svc, hosts := newCloudService(t, &stubTransport{})
	if _, err := svc.Import(context.Background(), ImportRequest{
		Hosts: []DiscoveredHost{
			{Name: "a", Host: "10.0.0.1", Username: "ubuntu"}, // no port
			{Name: "b", Host: "10.0.0.2"},                     // no username either
		},
		Username: "deploy", AuthMethod: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	saved, err := hosts.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range saved {
		// An explicit override replaces the provider default on every host.
		if h.Username != "deploy" {
			t.Errorf("%s username = %q, want the deploy override", h.Name, h.Username)
		}
		if h.Port != 22 {
			t.Errorf("%s port = %d, want the 22 default", h.Name, h.Port)
		}
		if h.AuthMethod != "agent" {
			t.Errorf("%s auth = %q, want agent", h.Name, h.AuthMethod)
		}
	}
}

// A host the store rejects must not abort the rest of the batch.
func TestImportReportsPerHostFailuresAndContinues(t *testing.T) {
	svc, hosts := newCloudService(t, &stubTransport{})
	result, err := svc.Import(context.Background(), ImportRequest{
		Hosts: []DiscoveredHost{
			{Name: "good", Host: "10.0.0.1", Username: "ops"},
			{Name: "", Host: "10.0.0.2", Username: "ops"}, // no name: invalid
			{Name: "also-good", Host: "10.0.0.3", Username: "ops"},
		},
	})
	if err != nil {
		t.Fatalf("a single bad host should not fail the call: %v", err)
	}
	if result.Imported != 2 {
		t.Errorf("imported = %d, want 2", result.Imported)
	}
	if len(result.Errors) != 1 {
		t.Errorf("errors = %v, want exactly one", result.Errors)
	}
	saved, _ := hosts.List()
	if len(saved) != 2 {
		t.Errorf("saved %d hosts, want 2", len(saved))
	}
}

func TestImportRejectsEmptyAndOversizedSelections(t *testing.T) {
	svc, _ := newCloudService(t, &stubTransport{})
	if _, err := svc.Import(context.Background(), ImportRequest{}); err == nil {
		t.Error("accepted an empty selection")
	}
	many := make([]DiscoveredHost, 1001)
	if _, err := svc.Import(context.Background(), ImportRequest{Hosts: many}); err == nil {
		t.Error("accepted more than 1000 hosts")
	}
}

func TestFirstLineTruncatesAndStopsAtNewline(t *testing.T) {
	if got := firstLine("  one\ntwo  ", 100); got != "one" {
		t.Errorf("got %q, want \"one\"", got)
	}
	if got := firstLine(strings.Repeat("x", 20), 5); got != "xxxxx…" {
		t.Errorf("got %q, want a truncated value", got)
	}
}
