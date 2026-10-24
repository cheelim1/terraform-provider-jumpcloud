package jumpcloud

// database_attributes is Optional+Computed, so once an application has
// mappings, Terraform has no way to clear them: the schema description says
// so, and this test pins that behavior down. If a future change makes clearing
// possible (for example an explicit opt-in flag), update this test and the
// description together.

import (
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

const dbAttrsConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_application" "a" {
  name          = "aws"
  display_label = "mappings"
  sso_url       = "aws-sso"
  idp_entity_id = "jc-idp"
  sp_entity_id  = "urn:amazon:webservices"
  acs_url       = "https://signin.aws.amazon.com/saml"
  %s
}
`

// dbAttrsRemoteRows counts the user attribute mapping rows the fake JumpCloud
// API currently holds for its only application.
func dbAttrsRemoteRows(f *fakeApplications) int {
	app := f.app()
	if app == nil {
		return -1
	}
	cfg, _ := app["config"].(map[string]interface{})
	field, _ := cfg["databaseAttributes"].(map[string]interface{})
	rows, _ := field["value"].([]interface{})
	return len(rows)
}

func TestApplicationDatabaseAttributesCannotBeClearedFromConfig(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	wantRemoteRows := func(n int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := dbAttrsRemoteRows(f); got != n {
				return fmt.Errorf("JumpCloud has %d database_attributes rows, want %d", got, n)
			}
			return nil
		}
	}
	notABlockArgument := regexp.MustCompile(`database_attributes" is not expected here`)

	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(dbAttrsConfig, `database_attributes {
    name  = "email"
    value = "email"
  }`),
				Check: wantRemoteRows(1),
			},
			{
				// Removing every block plans as no change and leaves the rows.
				Config: fmt.Sprintf(dbAttrsConfig, ``),
				Check:  wantRemoteRows(1),
			},
			{
				// `= []` is not valid for a block.
				Config:      fmt.Sprintf(dbAttrsConfig, `database_attributes = []`),
				ExpectError: notABlockArgument,
			},
			{
				// Neither is `= null`.
				Config:      fmt.Sprintf(dbAttrsConfig, `database_attributes = null`),
				ExpectError: notABlockArgument,
			},
			{
				// A dynamic block with an empty for_each is the same as no block.
				Config: fmt.Sprintf(dbAttrsConfig, `dynamic "database_attributes" {
    for_each = []
    content {
      name  = database_attributes.value.name
      value = database_attributes.value.value
    }
  }`),
				Check: wantRemoteRows(1),
			},
		},
	})
}
