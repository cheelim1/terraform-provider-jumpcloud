package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestAccDataSourceJumpCloudUser(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	testEmail := fmt.Sprintf("%s@testorg.com", rName)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceUserConfig(rName, testEmail),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.jumpcloud_user.test", "id"),
					resource.TestCheckResourceAttr("data.jumpcloud_user.test", "email", testEmail),
					resource.TestCheckResourceAttrSet("data.jumpcloud_user.test", "username"),
				),
			},
		},
	})
}

func testAccDataSourceUserConfig(name, email string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
			username  = "%s"
			email     = "%s"
			firstname = "Test"
			lastname  = "User"
		}

		data "jumpcloud_user" "test" {
			email = jumpcloud_user.test_user.email
		}
	`, name, email)
}
