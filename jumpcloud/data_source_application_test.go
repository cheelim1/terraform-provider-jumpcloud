package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestDataSourceApplicationBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testDataSourceApplicationConfigBasic(rName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jumpcloud_application.test_app", "display_name", fmt.Sprintf("test_app_%s", rName)),
				),
			},
		},
	})
}

func testDataSourceApplicationConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_application" "test_app" {
			display_name = "test_app_%[1]s"
  			sso_url      = "https://sso.jumpcloud.com/saml2/test_app_%[1]s"
		}

		data "jumpcloud_application" "test_app" {
			display_name = jumpcloud_application.test_app.display_name
		}
	`, name)
}
