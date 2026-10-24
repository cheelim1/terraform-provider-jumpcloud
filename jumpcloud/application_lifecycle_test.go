package jumpcloud

// Drives real Terraform core (in-process, via SDK v1's helper/resource)
// through the jumpcloud_application lifecycle against a stateful fake
// JumpCloud v1 applications API. Hermetic: no network, no TF_ACC (the
// metadata-XML fetch goes to the real console URL, fails without an org_id,
// and is skipped with a warning, exactly as in production without org_id).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

// applicationTestProviders points the provider at url: v2 calls at url+"/v2"
// and, via convertV2toV1Config, v1 calls (applications) at url itself.
func applicationTestProviders(url string) map[string]terraform.ResourceProvider {
	p := Provider()
	p.ConfigureFunc = func(d *schema.ResourceData) (interface{}, error) {
		c := jcapiv2.NewConfiguration()
		c.BasePath = url + "/v2"
		c.AddDefaultHeader("x-api-key", d.Get("api_key").(string))
		return c, nil
	}
	return map[string]terraform.ResourceProvider{"jumpcloud": p}
}

type fakeApplications struct {
	mu     sync.Mutex
	apps   map[string]map[string]interface{}
	bodies []map[string]interface{} // every POST/PUT body received
	nextID int
}

func (f *fakeApplications) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	id := strings.TrimPrefix(r.URL.Path, "/applications/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/applications":
		f.bodies = append(f.bodies, deepCopyJSON(body))
		f.nextID++
		body["_id"] = fmt.Sprintf("app%d", f.nextID)
		f.apps[body["_id"].(string)] = body
		_ = json.NewEncoder(w).Encode(body)
	case r.Method == http.MethodGet && f.apps[id] != nil:
		_ = json.NewEncoder(w).Encode(f.apps[id])
	case r.Method == http.MethodPut && f.apps[id] != nil:
		f.bodies = append(f.bodies, deepCopyJSON(body))
		body["_id"] = id
		f.apps[id] = body
		_ = json.NewEncoder(w).Encode(body)
	case r.Method == http.MethodDelete && f.apps[id] != nil:
		delete(f.apps, id)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func deepCopyJSON(v map[string]interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	return out
}

func (f *fakeApplications) app() map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.apps {
		return deepCopyJSON(a)
	}
	return nil
}

func (f *fakeApplications) lastBody() map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[len(f.bodies)-1]
}

// consoleEdits are settings an admin made in the JumpCloud console on an
// application Terraform created with the master (pre-#30) provider, which
// exposed none of them: SSO activated, user attribute mappings, group
// attribute, and response signing.
func applyConsoleEdits(app map[string]interface{}) {
	app["active"] = true
	cfg := app["config"].(map[string]interface{})
	cfg["databaseAttributes"] = map[string]interface{}{"type": "constantAttributes", "label": "User Attributes:", "position": float64(9),
		"value": []interface{}{
			map[string]interface{}{"name": "email", "value": "email", "isCustomAttribute": false, "mutable": false, "readOnly": true, "tempId": "console-1"},
			map[string]interface{}{"name": "firstName", "value": "firstname", "isCustomAttribute": true, "mutable": true, "readOnly": false, "tempId": "console-2"},
		}}
	cfg["includeGroups"] = map[string]interface{}{"type": "bool", "value": true}
	cfg["groupsAttributeName"] = map[string]interface{}{"type": "text", "value": "memberOf"}
	cfg["signResponse"] = map[string]interface{}{"type": "bool", "value": true}
	cfg["signAssertion"] = map[string]interface{}{"type": "bool", "value": true}
	cfg["declareRedirectEndpoint"] = map[string]interface{}{"type": "bool", "value": true}
	cfg["signatureAlgorithm"] = map[string]interface{}{"type": "select", "value": "RSA-SHA256"}
}

// An application config as written for the master provider: none of the
// fields #30 adds.
const applicationLegacyConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_application" "a" {
  name          = "aws"
  display_label = "%s"
  sso_url       = "aws-sso"
  idp_entity_id = "jc-idp"
  sp_entity_id  = "urn:amazon:webservices"
  acs_url       = "https://signin.aws.amazon.com/saml"
  constant_attributes {
    name  = "https://aws.amazon.com/SAML/Attributes/SessionDuration"
    value = "3600"
  }
}
`

// Upgrading an existing user to this provider version must not plan to undo
// what they configured in the console, and an unrelated change must leave
// those settings exactly as they were.
func TestApplicationUpgradeDoesNotRevertConsoleSettings(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	var before map[string]interface{}
	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: fmt.Sprintf(applicationLegacyConfig, "AWS")},
			{
				PreConfig: func() {
					f.mu.Lock()
					for _, a := range f.apps {
						applyConsoleEdits(a)
					}
					f.mu.Unlock()
					before = f.app()
				},
				Config:   fmt.Sprintf(applicationLegacyConfig, "AWS"),
				PlanOnly: true, // must be an empty plan
			},
			{
				Config: fmt.Sprintf(applicationLegacyConfig, "AWS (renamed)"),
				Check: func(*terraform.State) error {
					after := f.app()
					if after["displayLabel"] != "AWS (renamed)" {
						return fmt.Errorf("rename not applied: %v", after["displayLabel"])
					}
					if after["active"] != true {
						return fmt.Errorf("SSO was deactivated by an unrelated change")
					}
					bc, ac := before["config"].(map[string]interface{}), after["config"].(map[string]interface{})
					for _, k := range []string{"databaseAttributes", "includeGroups", "groupsAttributeName", "signResponse", "signAssertion", "declareRedirectEndpoint", "signatureAlgorithm"} {
						if !reflect.DeepEqual(bc[k], ac[k]) {
							return fmt.Errorf("console setting %s changed by an unrelated update:\n before %v\n after  %v", k, bc[k], ac[k])
						}
					}
					return nil
				},
			},
		},
	})
}

const applicationManagedConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_application" "a" {
  name                      = "claude"
  display_label             = "Claude"
  sso_url                   = "claude-sso"
  idp_entity_id             = "jc-idp"
  sp_entity_id              = "https://claude.ai"
  acs_url                   = "https://claude.ai/sso/acs"
  idp_private_key           = "-----BEGIN PRIVATE KEY-----secret-----END PRIVATE KEY-----"
  active                    = %s
  include_groups            = true
  groups_attribute_name     = "groups"
  sign_response             = true
  declare_redirect_endpoint = true
  database_attributes {
    name  = "email"
    value = "email"
  }
}
`

// The new settings work when managed: created as configured, no drift, an
// import round-trips, and a change is applied.
func TestApplicationManagedSettingsLifecycle(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	cfgField := func(app map[string]interface{}, key string) interface{} {
		field, _ := app["config"].(map[string]interface{})[key].(map[string]interface{})
		return field["value"]
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		CheckDestroy: func(*terraform.State) error {
			if f.app() != nil {
				return fmt.Errorf("application not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(applicationManagedConfig, "true"),
				Check: func(*terraform.State) error {
					a := f.app()
					if a["active"] != true || cfgField(a, "includeGroups") != true || cfgField(a, "groupsAttributeName") != "groups" ||
						cfgField(a, "signResponse") != true || cfgField(a, "declareRedirectEndpoint") != true {
						return fmt.Errorf("managed settings not sent on create: %v", a)
					}
					if _, sent := a["config"].(map[string]interface{})["signAssertion"]; sent {
						return fmt.Errorf("unset sign_assertion must be left to JumpCloud's default, not sent")
					}
					if _, sent := a["config"].(map[string]interface{})["idpCertificate"]; sent {
						return fmt.Errorf("unset idp_certificate must be omitted")
					}
					rows := cfgField(a, "databaseAttributes").([]interface{})
					if len(rows) != 1 || rows[0].(map[string]interface{})["name"] != "email" {
						return fmt.Errorf("database attributes not sent: %v", rows)
					}
					return nil
				},
			},
			{
				ResourceName: "jumpcloud_application.a", Config: fmt.Sprintf(applicationManagedConfig, "true"),
				ImportState: true, ImportStateVerify: true,
				// Never refreshed by Read: the API returns it empty.
				ImportStateVerifyIgnore: []string{"idp_private_key"},
			},
			{
				Config: fmt.Sprintf(applicationManagedConfig, "false"),
				Check: func(*terraform.State) error {
					if f.app()["active"] != false {
						return fmt.Errorf("active=false not applied")
					}
					return nil
				},
			},
		},
	})
}

// A legacy config creating a brand-new application sends exactly the fields
// the master provider sent (plus nothing #30 added), so JumpCloud's own
// defaults apply to everything else.
func TestApplicationCreateWithoutNewSettingsOmitsThem(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(applicationLegacyConfig, "AWS"),
			Check: func(*terraform.State) error {
				body := f.lastBody()
				if _, ok := body["active"]; ok {
					return fmt.Errorf("active sent on create without being configured")
				}
				cfg := body["config"].(map[string]interface{})
				for _, k := range []string{"databaseAttributes", "includeGroups", "groupsAttributeName", "signAssertion", "signResponse", "declareRedirectEndpoint", "idpCertificate", "idpPrivateKey"} {
					if _, ok := cfg[k]; ok {
						return fmt.Errorf("%s sent on create without being configured", k)
					}
				}
				for _, k := range []string{"idpEntityId", "spEntityId", "acsUrl", "constantAttributes"} {
					if _, ok := cfg[k]; !ok {
						return fmt.Errorf("%s missing from create body", k)
					}
				}
				return nil
			},
		}},
	})
}
