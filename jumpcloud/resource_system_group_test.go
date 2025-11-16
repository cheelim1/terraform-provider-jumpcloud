package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestAccSystemGroup(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testAccSystemGroup(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_system_group.test_group", "name", rName),
					resource.TestCheckResourceAttrSet("jumpcloud_system_group.test_group", "jc_id"),
				),
			},
		},
	})
}

func testAccSystemGroup(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_system_group" "test_group" {
    		name = "%s"
		}`, name)
}
