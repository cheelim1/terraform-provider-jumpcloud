package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestApplicationResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testApplicationResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_application.test_app", "display_name", fmt.Sprintf("test_app_%s", rName)),
				),
			},
		},
	})
}

func testApplicationResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
resource "jumpcloud_application" "test_app" {
  display_name = "test_app_%[1]s"
  sso_url      = "https://sso.jumpcloud.com/saml2/test_app_%[1]s"
}
`, name)
}
