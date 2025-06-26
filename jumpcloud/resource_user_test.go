package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestUserResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "email", rName+"@testorg.com"),
				),
			},
		},
	})
}

func testUserResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
  			username = "%s"
			email = "%s@testorg.com"
		}`, name, name,
	)
}
