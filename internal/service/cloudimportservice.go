package service

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blacknode/blacknode/internal/store"
)

// CloudImportService discovers instances from cloud providers so hosts do not
// have to be typed in by hand.
//
// Implemented directly against each provider's REST API rather than its SDK:
// no new module dependencies are available here, and discovery needs one
// read-only call per provider, which is far less code than an SDK would pull
// in. Credentials are used for the single call and never stored — the user
// re-enters them (or pastes a short-lived token) each time.
type CloudImportService struct {
	hosts *store.Hosts
	// client is overridable so tests can serve recorded provider responses
	// without reaching the network.
	client *http.Client
}

func NewCloudImportService(hosts *store.Hosts) *CloudImportService {
	return &CloudImportService{
		hosts: hosts,
		// Discovery is interactive: fail fast rather than hang the dialog.
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// DiscoveredHost is a candidate the user can review before anything is saved.
// Nothing is written to the database until Import is called.
type DiscoveredHost struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	// Username is the provider's conventional default where there is one, and
	// empty otherwise. The import dialog lets the user override it.
	Username string   `json:"username"`
	Tags     []string `json:"tags"`
	// Platform is prefilled from the provider's image metadata when it says
	// something useful, so the host list badge is right before first connect.
	Platform string `json:"platform,omitempty"`
	Region   string `json:"region,omitempty"`
	// Detail is a short human string (instance type, size) for the review list.
	Detail string `json:"detail,omitempty"`
	// AlreadyExists marks a candidate whose address is already a saved host, so
	// the dialog can pre-deselect it instead of creating a duplicate.
	AlreadyExists bool `json:"alreadyExists"`
}

// CloudCredentials carries whatever the chosen provider needs. Only the fields
// relevant to Provider are read.
type CloudCredentials struct {
	Provider string `json:"provider"` // "aws" | "digitalocean" | "azure"

	// AWS
	AccessKeyID     string `json:"accessKeyID,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	SessionToken    string `json:"sessionToken,omitempty"`
	Region          string `json:"region,omitempty"`

	// DigitalOcean
	Token string `json:"token,omitempty"`

	// Azure
	TenantID       string `json:"tenantID,omitempty"`
	ClientID       string `json:"clientID,omitempty"`
	ClientSecret   string `json:"clientSecret,omitempty"`
	SubscriptionID string `json:"subscriptionID,omitempty"`
}

// Discover queries the provider and returns candidates without saving any.
func (s *CloudImportService) Discover(ctx context.Context, creds CloudCredentials) ([]DiscoveredHost, error) {
	var (
		found []DiscoveredHost
		err   error
	)
	switch strings.ToLower(strings.TrimSpace(creds.Provider)) {
	case "aws":
		found, err = s.discoverAWS(ctx, creds)
	case "digitalocean", "do":
		found, err = s.discoverDigitalOcean(ctx, creds)
	case "azure":
		found, err = s.discoverAzure(ctx, creds)
	default:
		return nil, fmt.Errorf("unsupported provider %q", creds.Provider)
	}
	if err != nil {
		return nil, err
	}
	return s.markExisting(found)
}

// markExisting flags candidates whose address already belongs to a saved host.
func (s *CloudImportService) markExisting(found []DiscoveredHost) ([]DiscoveredHost, error) {
	existing, err := s.hosts.List()
	if err != nil {
		return nil, err
	}
	addresses := make(map[string]bool, len(existing))
	names := make(map[string]bool, len(existing))
	for _, h := range existing {
		addresses[strings.ToLower(h.Host)] = true
		names[strings.ToLower(h.Name)] = true
	}
	for i := range found {
		if addresses[strings.ToLower(found[i].Host)] || names[strings.ToLower(found[i].Name)] {
			found[i].AlreadyExists = true
		}
	}
	return found, nil
}

// ImportRequest is the reviewed selection to save.
type ImportRequest struct {
	Hosts []DiscoveredHost `json:"hosts"`
	// Group, Username, AuthMethod and KeyID are applied to every imported
	// host, so a fleet lands ready to connect. Username falls back to each
	// candidate's provider default when left empty.
	Group      string `json:"group,omitempty"`
	Username   string `json:"username,omitempty"`
	AuthMethod string `json:"authMethod,omitempty"`
	KeyID      string `json:"keyID,omitempty"`
}

// ImportResult reports what happened, per host, so a partial failure is
// visible rather than swallowed.
type ImportResult struct {
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
}

// Import saves the selected candidates. Hosts already present are skipped
// rather than duplicated, and one failure does not abort the rest.
func (s *CloudImportService) Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	if len(req.Hosts) == 0 {
		return ImportResult{}, errors.New("select at least one host to import")
	}
	if len(req.Hosts) > 1000 {
		return ImportResult{}, errors.New("import at most 1000 hosts at a time")
	}

	checked, err := s.markExisting(append([]DiscoveredHost(nil), req.Hosts...))
	if err != nil {
		return ImportResult{}, err
	}

	result := ImportResult{}
	for _, candidate := range checked {
		if candidate.AlreadyExists {
			result.Skipped++
			continue
		}
		username := req.Username
		if username == "" {
			username = candidate.Username
		}
		port := candidate.Port
		if port == 0 {
			port = 22
		}
		authMethod := req.AuthMethod
		if authMethod == "" {
			authMethod = "key"
		}
		_, err := s.hosts.Create(store.Host{
			Name:       candidate.Name,
			Host:       candidate.Host,
			Port:       port,
			Username:   username,
			AuthMethod: authMethod,
			KeyID:      req.KeyID,
			Group:      req.Group,
			Tags:       candidate.Tags,
			Platform:   candidate.Platform,
			Protocol:   "ssh",
		})
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", candidate.Name, err))
			continue
		}
		result.Imported++
	}
	return result, nil
}

// ── AWS EC2 ─────────────────────────────────────────────────────────────

// ec2DescribeInstancesResponse is the subset of the DescribeInstances XML
// response that discovery needs.
type ec2DescribeInstancesResponse struct {
	NextToken      string `xml:"nextToken"`
	ReservationSet []struct {
		InstancesSet []struct {
			InstanceID   string `xml:"instanceId"`
			InstanceType string `xml:"instanceType"`
			PublicIP     string `xml:"ipAddress"`
			PrivateIP    string `xml:"privateIpAddress"`
			PublicDNS    string `xml:"dnsName"`
			Placement    struct {
				AvailabilityZone string `xml:"availabilityZone"`
			} `xml:"placement"`
			InstanceState struct {
				Name string `xml:"name"`
			} `xml:"instanceState"`
			Platform string `xml:"platformDetails"`
			TagSet   []struct {
				Key   string `xml:"key"`
				Value string `xml:"value"`
			} `xml:"tagSet>item"`
		} `xml:"instancesSet>item"`
	} `xml:"reservationSet>item"`
}

func (s *CloudImportService) discoverAWS(ctx context.Context, creds CloudCredentials) ([]DiscoveredHost, error) {
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return nil, errors.New("AWS access key ID and secret access key are required")
	}
	region := creds.Region
	if region == "" {
		region = "us-east-1"
	}

	out := []DiscoveredHost{}
	nextToken := ""
	// Bounded rather than while(true): a malformed nextToken loop would
	// otherwise spin against a billed API.
	for page := 0; page < 20; page++ {
		query := url.Values{
			"Action":  {"DescribeInstances"},
			"Version": {"2016-11-15"},
			// Only running instances are connectable.
			"Filter.1.Name":    {"instance-state-name"},
			"Filter.1.Value.1": {"running"},
			"MaxResults":       {"1000"},
		}
		if nextToken != "" {
			query.Set("NextToken", nextToken)
		}

		endpoint := fmt.Sprintf("https://ec2.%s.amazonaws.com/?%s", region, query.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		signAWSRequest(req, awsCredentials{
			AccessKeyID:     creds.AccessKeyID,
			SecretAccessKey: creds.SecretAccessKey,
			SessionToken:    creds.SessionToken,
			Region:          region,
		}, "ec2", time.Now())

		body, err := s.do(req, "AWS EC2")
		if err != nil {
			return nil, err
		}

		var parsed ec2DescribeInstancesResponse
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("parse EC2 response: %w", err)
		}
		out = append(out, ec2Hosts(parsed, region)...)

		if parsed.NextToken == "" {
			break
		}
		nextToken = parsed.NextToken
	}
	return out, nil
}

// ec2Hosts flattens the reservation/instance nesting into candidates.
func ec2Hosts(parsed ec2DescribeInstancesResponse, region string) []DiscoveredHost {
	out := []DiscoveredHost{}
	for _, reservation := range parsed.ReservationSet {
		for _, instance := range reservation.InstancesSet {
			// Prefer the public DNS name over a bare IP: it survives a stop/start
			// that changes the address, which an imported host should too.
			address := instance.PublicDNS
			if address == "" {
				address = instance.PublicIP
			}
			if address == "" {
				address = instance.PrivateIP
			}
			if address == "" {
				continue
			}

			name := instance.InstanceID
			tags := []string{}
			for _, tag := range instance.TagSet {
				if strings.EqualFold(tag.Key, "Name") && tag.Value != "" {
					name = tag.Value
					continue
				}
				if tag.Value != "" && !strings.HasPrefix(tag.Key, "aws:") {
					tags = append(tags, strings.ToLower(tag.Key+":"+tag.Value))
				}
			}

			out = append(out, DiscoveredHost{
				Name:     name,
				Host:     address,
				Port:     22,
				Username: awsDefaultUser(instance.Platform),
				Tags:     tags,
				Platform: normalizePlatform(awsPlatformID(instance.Platform)),
				Region:   instance.Placement.AvailabilityZone,
				Detail:   strings.TrimSpace(instance.InstanceType + " · " + instance.InstanceID),
			})
		}
	}
	return out
}

// awsDefaultUser maps platformDetails to the login AWS's own AMIs use. Left
// empty when unknown rather than guessed, so the dialog prompts for it.
func awsDefaultUser(platformDetails string) string {
	switch {
	case strings.Contains(strings.ToLower(platformDetails), "windows"):
		return "" // no SSH login by convention
	case strings.Contains(strings.ToLower(platformDetails), "red hat"):
		return "ec2-user"
	case strings.Contains(strings.ToLower(platformDetails), "suse"):
		return "ec2-user"
	case strings.Contains(strings.ToLower(platformDetails), "ubuntu"):
		return "ubuntu"
	case strings.Contains(strings.ToLower(platformDetails), "linux/unix"):
		// Amazon Linux reports as Linux/UNIX and uses ec2-user.
		return "ec2-user"
	}
	return ""
}

func awsPlatformID(platformDetails string) string {
	l := strings.ToLower(platformDetails)
	switch {
	case strings.Contains(l, "windows"):
		return "windows"
	case strings.Contains(l, "red hat"):
		return "rhel"
	case strings.Contains(l, "suse"):
		return "suse"
	case strings.Contains(l, "ubuntu"):
		return "ubuntu"
	case strings.Contains(l, "debian"):
		return "debian"
	}
	return ""
}

// ── DigitalOcean ────────────────────────────────────────────────────────

type doDropletsResponse struct {
	Droplets []struct {
		ID     int64    `json:"id"`
		Name   string   `json:"name"`
		Status string   `json:"status"`
		Tags   []string `json:"tags"`
		Region struct {
			Slug string `json:"slug"`
		} `json:"region"`
		Size struct {
			Slug string `json:"slug"`
		} `json:"size"`
		Image struct {
			Distribution string `json:"distribution"`
		} `json:"image"`
		Networks struct {
			V4 []struct {
				IPAddress string `json:"ip_address"`
				Type      string `json:"type"`
			} `json:"v4"`
		} `json:"networks"`
	} `json:"droplets"`
	Links struct {
		Pages struct {
			Next string `json:"next"`
		} `json:"pages"`
	} `json:"links"`
}

func (s *CloudImportService) discoverDigitalOcean(ctx context.Context, creds CloudCredentials) ([]DiscoveredHost, error) {
	if creds.Token == "" {
		return nil, errors.New("a DigitalOcean API token is required")
	}

	out := []DiscoveredHost{}
	next := "https://api.digitalocean.com/v2/droplets?per_page=200"
	for page := 0; page < 20 && next != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+creds.Token)

		body, err := s.do(req, "DigitalOcean")
		if err != nil {
			return nil, err
		}
		var parsed doDropletsResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("parse DigitalOcean response: %w", err)
		}

		for _, droplet := range parsed.Droplets {
			if droplet.Status != "active" {
				continue
			}
			address := ""
			for _, net := range droplet.Networks.V4 {
				if net.Type == "public" {
					address = net.IPAddress
					break
				}
			}
			if address == "" {
				// Fall back to a private address so VPC-only droplets still
				// import; they are reachable through a bastion.
				for _, net := range droplet.Networks.V4 {
					if net.IPAddress != "" {
						address = net.IPAddress
						break
					}
				}
			}
			if address == "" {
				continue
			}
			out = append(out, DiscoveredHost{
				Name:     droplet.Name,
				Host:     address,
				Port:     22,
				Username: "root", // DigitalOcean images ship with root SSH
				Tags:     droplet.Tags,
				Platform: normalizePlatform(droplet.Image.Distribution),
				Region:   droplet.Region.Slug,
				Detail:   droplet.Size.Slug,
			})
		}
		next = parsed.Links.Pages.Next
	}
	return out, nil
}

// ── Azure ───────────────────────────────────────────────────────────────

// Azure needs two calls: an OAuth2 client-credentials token, then one Resource
// Graph query. Resource Graph is used rather than the Compute API because a VM
// there does not carry its own address — it would take a follow-up call per
// NIC and per public IP, where this joins them server-side in one request.
const azureResourceGraphQuery = `Resources
| where type =~ 'microsoft.compute/virtualmachines'
| extend nicId = tostring(properties.networkProfile.networkInterfaces[0].id)
| join kind=leftouter (
    Resources
    | where type =~ 'microsoft.network/networkinterfaces'
    | extend nicId = id
    | extend privateIp = tostring(properties.ipConfigurations[0].properties.privateIPAddress)
    | extend publicIpId = tostring(properties.ipConfigurations[0].properties.publicIPAddress.id)
    | project nicId, privateIp, publicIpId
) on nicId
| join kind=leftouter (
    Resources
    | where type =~ 'microsoft.network/publicipaddresses'
    | extend publicIpId = id
    | extend publicIp = tostring(properties.ipAddress)
    | extend fqdn = tostring(properties.dnsSettings.fqdn)
    | project publicIpId, publicIp, fqdn
) on publicIpId
| project name, location, tags, privateIp, publicIp, fqdn,
    vmSize = tostring(properties.hardwareProfile.vmSize),
    offer = tostring(properties.storageProfile.imageReference.offer),
    osType = tostring(properties.storageProfile.osDisk.osType),
    adminUser = tostring(properties.osProfile.adminUsername)
| limit 1000`

type azureTokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

type azureGraphResponse struct {
	Data []struct {
		Name      string            `json:"name"`
		Location  string            `json:"location"`
		Tags      map[string]string `json:"tags"`
		PrivateIP string            `json:"privateIp"`
		PublicIP  string            `json:"publicIp"`
		FQDN      string            `json:"fqdn"`
		VMSize    string            `json:"vmSize"`
		Offer     string            `json:"offer"`
		OSType    string            `json:"osType"`
		AdminUser string            `json:"adminUser"`
	} `json:"data"`
}

func (s *CloudImportService) discoverAzure(ctx context.Context, creds CloudCredentials) ([]DiscoveredHost, error) {
	if creds.TenantID == "" || creds.ClientID == "" || creds.ClientSecret == "" || creds.SubscriptionID == "" {
		return nil, errors.New("Azure tenant ID, client ID, client secret and subscription ID are required")
	}

	token, err := s.azureToken(ctx, creds)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"subscriptions": []string{creds.SubscriptionID},
		"query":         azureResourceGraphQuery,
		// resultFormat must be set explicitly: the REST API defaults to "table"
		// (data as {columns, rows}), which azureGraphResponse cannot decode.
		// objectArray returns data as [{...}], one object per VM — the shape the
		// parser and the query's `project` list expect.
		"options": map[string]any{"resultFormat": "objectArray"},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://management.azure.com/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01",
		strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	body, err := s.do(req, "Azure Resource Graph")
	if err != nil {
		return nil, err
	}
	var parsed azureGraphResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse Azure response: %w", err)
	}

	out := []DiscoveredHost{}
	for _, vm := range parsed.Data {
		address := vm.FQDN
		if address == "" {
			address = vm.PublicIP
		}
		if address == "" {
			address = vm.PrivateIP
		}
		if address == "" {
			continue
		}
		tags := make([]string, 0, len(vm.Tags))
		for k, v := range vm.Tags {
			if v != "" {
				tags = append(tags, strings.ToLower(k+":"+v))
			}
		}
		if strings.EqualFold(vm.OSType, "Windows") {
			// No SSH by convention; still listed so the user can see it.
			vm.AdminUser = ""
		}
		out = append(out, DiscoveredHost{
			Name:     vm.Name,
			Host:     address,
			Port:     22,
			Username: vm.AdminUser,
			Tags:     tags,
			Platform: normalizePlatform(vm.Offer),
			Region:   vm.Location,
			Detail:   vm.VMSize,
		})
	}
	return out, nil
}

func (s *CloudImportService) azureToken(ctx context.Context, creds CloudCredentials) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {creds.ClientID},
		"client_secret": {creds.ClientSecret},
		"resource":      {"https://management.azure.com/"},
	}
	endpoint := "https://login.microsoftonline.com/" + url.PathEscape(creds.TenantID) + "/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	body, err := s.do(req, "Azure sign-in")
	if err != nil {
		return "", err
	}
	var parsed azureTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse Azure token response: %w", err)
	}
	if parsed.AccessToken == "" {
		if parsed.ErrorDesc != "" {
			// Azure's error_description is multi-line and starts with a
			// correlation ID; the first line is the actionable part.
			return "", fmt.Errorf("Azure sign-in failed: %s", strings.SplitN(parsed.ErrorDesc, "\n", 2)[0])
		}
		return "", errors.New("Azure sign-in returned no access token")
	}
	return parsed.AccessToken, nil
}

// ── shared ──────────────────────────────────────────────────────────────

// maxCloudResponse caps how much of a provider response is read. Discovery
// responses are well under this; the limit stops a misdirected endpoint from
// exhausting memory.
const maxCloudResponse = 16 << 20 // 16 MiB

// do performs the request and returns the body, turning a non-2xx into an
// error that includes the provider's own message — which is what actually
// tells the user their credentials are wrong.
func (s *CloudImportService) do(req *http.Request, provider string) ([]byte, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", provider, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCloudResponse))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", provider, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s returned %s: %s", provider, resp.Status, firstLine(string(body), 400))
	}
	return body, nil
}

// firstLine trims a provider error body down to something displayable.
func firstLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
