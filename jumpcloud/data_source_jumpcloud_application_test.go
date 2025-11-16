package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestAccDataSourceJumpCloudApplication_basic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	displayLabel := fmt.Sprintf("test-app-%s", rName)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceJumpCloudApplicationConfig(rName, displayLabel),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.jumpcloud_application.test_application", "id"),
					resource.TestCheckResourceAttr("data.jumpcloud_application.test_application", "display_label", displayLabel),
				),
			},
		},
	})
}

func testAccDataSourceJumpCloudApplicationConfig(randSuffix, displayLabel string) string {
	return fmt.Sprintf(`
resource "jumpcloud_application" "test_application" {
	name            = "test-app-%s"
	display_label   = "%s"
	sso_url         = "https://sso.jumpcloud.com/saml2/test-application-%s"
	idp_certificate = "-----BEGIN CERTIFICATE-----\nTEST_CERTIFICATE\n-----END CERTIFICATE-----"
	idp_entity_id   = "https://test-idp.example.com"
	idp_private_key  = "-----BEGIN PRIVATE KEY-----\nTEST_PRIVATE_KEY\n-----END PRIVATE KEY-----"
	sp_entity_id     = "https://test-sp.example.com"
	acs_url          = "https://test-sp.example.com/acs"
}

data "jumpcloud_application" "test_application" {
	display_label = jumpcloud_application.test_application.display_label
}`, randSuffix, displayLabel, randSuffix)
}
