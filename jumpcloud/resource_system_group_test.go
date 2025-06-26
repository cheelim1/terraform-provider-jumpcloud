package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestSystemGroupResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testSystemGroupResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_system_group.test_group", "name", rName),
				),
			},
		},
	})
}

func testSystemGroupResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_system_group" "test_group" {
  			name = "%s"
		}`, name,
	)
}
