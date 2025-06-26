package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestDataSourceUserGroupBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:  func() { testAccPreCheck(t) },
		Providers: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: testDataSourceUserGroupConfigBasic(rName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test_group", "name", rName),
				),
			},
		},
	})
}

func testDataSourceUserGroupConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user_group" "test_group" {
			name = "%s"
		}

		data "jumpcloud_user_group" "test_group" {
			name = jumpcloud_user_group.test_group.name
		}
	`, name)
}
