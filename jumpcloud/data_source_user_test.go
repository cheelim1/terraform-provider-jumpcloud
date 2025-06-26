package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestDataSourceUserBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testDataSourceUserConfigBasic(rName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("data.jumpcloud_user.test_user", "email", rName+"@testorg.com"),
				),
			},
		},
	})
}

func testDataSourceUserConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
			username = "%s"
			email    = "%s@testorg.com"
		}

		data "jumpcloud_user" "test_user" {
			username = jumpcloud_user.test_user.username
		}
	`, name, name)
}
