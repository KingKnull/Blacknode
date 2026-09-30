package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// azurePager serves the OAuth token, then two Resource Graph pages: the first
// carries a $skipToken, the second does not. It records the $skipToken seen on
// each graph call so the test can assert the continuation was echoed back.
type azurePager struct {
	graphCalls int
	sawToken   []bool
}

func (p *azurePager) RoundTrip(req *http.Request) (*http.Response, error) {
	ok := func(s string) *http.Response {
		return &http.Response{
			StatusCode: 200, Status: "200 OK",
			Body:   io.NopCloser(strings.NewReader(s)),
			Header: make(http.Header),
		}
	}
	switch {
	case strings.Contains(req.URL.String(), "login.microsoftonline.com"):
		return ok(azureTokenBody), nil
	case strings.Contains(req.URL.String(), "management.azure.com"):
		raw, _ := io.ReadAll(req.Body)
		p.sawToken = append(p.sawToken, strings.Contains(string(raw), `"$skipToken":"more"`))
		p.graphCalls++
		if p.graphCalls == 1 {
			return ok(azureGraphPage1), nil
		}
		return ok(azureGraphPage2), nil
	}
	return &http.Response{
		StatusCode: 404, Status: "404 Not Found",
		Body:   io.NopCloser(strings.NewReader(`{}`)),
		Header: make(http.Header),
	}, nil
}

const azureGraphPage1 = `{"$skipToken":"more","data":[
  {"name":"vm-a","location":"eastus","fqdn":"a.example.com","osType":"Linux","adminUser":"azureuser"}
]}`

const azureGraphPage2 = `{"data":[
  {"name":"vm-b","location":"eastus","fqdn":"b.example.com","osType":"Linux","adminUser":"azureuser"}
]}`

// TestDiscoverAzurePaginatesSkipToken is the regression test for Azure
// pagination: results past the first page must be followed via $skipToken
// rather than silently dropped (the query previously had a hard `| limit 1000`
// and made a single call).
func TestDiscoverAzurePaginatesSkipToken(t *testing.T) {
	pager := &azurePager{}
	svc, _ := newCloudService(t, pager)

	found, err := svc.Discover(context.Background(), CloudCredentials{
		Provider: "azure", TenantID: "t", ClientID: "c", ClientSecret: "s", SubscriptionID: "sub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d hosts across pages, want 2: %+v", len(found), found)
	}
	if found[0].Host != "a.example.com" || found[1].Host != "b.example.com" {
		t.Errorf("hosts = %q, %q; want a/b from both pages", found[0].Host, found[1].Host)
	}
	if pager.graphCalls != 2 {
		t.Fatalf("graph calls = %d, want 2 (one per page)", pager.graphCalls)
	}
	// First call carries no continuation; the second must echo page 1's token.
	if len(pager.sawToken) != 2 || pager.sawToken[0] || !pager.sawToken[1] {
		t.Errorf("skipToken presence per call = %v, want [false true]", pager.sawToken)
	}
}

// TestImportDeduplicatesWithinBatch is the regression test for intra-batch
// dedup: two discovered hosts sharing a name or an address must not both be
// created (markExisting only compares against already-saved hosts).
func TestImportDeduplicatesWithinBatch(t *testing.T) {
	svc, hosts := newCloudService(t, &stubTransport{})

	res, err := svc.Import(context.Background(), ImportRequest{
		Hosts: []DiscoveredHost{
			{Name: "web", Host: "1.1.1.1", Port: 22, Username: "u"},
			{Name: "web", Host: "2.2.2.2", Port: 22, Username: "u"}, // duplicate name
			{Name: "db", Host: "1.1.1.1", Port: 22, Username: "u"},  // duplicate address
			{Name: "cache", Host: "3.3.3.3", Port: 22, Username: "u"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 || res.Skipped != 2 {
		t.Fatalf("imported=%d skipped=%d, want imported=2 skipped=2", res.Imported, res.Skipped)
	}
	list, err := hosts.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("saved %d hosts, want 2 (web + cache)", len(list))
	}
}
