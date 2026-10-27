package jumpcloud

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

const learnMoreConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_application" "a" {
  name          = "aws"
  display_label = "AWS"
  sso_url       = "aws-sso"
  idp_entity_id = "jc-idp"
  sp_entity_id  = "urn:amazon:webservices"
  acs_url       = "https://signin.aws.amazon.com/saml"
  %s
}
`

// learn_more is sent on create and update (it never was), and omitting it
// leaves a value set in JumpCloud alone.
func TestApplicationLearnMore(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	want := func(v interface{}) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := f.app()["learnMore"]; got != v {
				return fmt.Errorf("learnMore = %v, want %v", got, v)
			}
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: fmt.Sprintf(learnMoreConfig, `learn_more = "https://example.com/a"`), Check: want("https://example.com/a")},
			{Config: fmt.Sprintf(learnMoreConfig, `learn_more = "https://example.com/b"`), Check: want("https://example.com/b")},
			{Config: fmt.Sprintf(learnMoreConfig, ``), Check: want("https://example.com/b")}, // omitted: left alone
			{
				PreConfig: func() {
					f.mu.Lock()
					for _, a := range f.apps {
						a["learnMore"] = "https://console-set.example.com"
					}
					f.mu.Unlock()
				},
				Config: fmt.Sprintf(learnMoreConfig, ``), PlanOnly: true, // no diff for a console value
			},
		},
	})
}
