package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestUserGroupAssociationResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserGroupAssociationResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("jumpcloud_user_group_association.test_assoc", "group_id"),
					resource.TestCheckResourceAttrSet("jumpcloud_user_group_association.test_assoc", "object_id"),
				),
			},
		},
	})
}

func testUserGroupAssociationResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
resource "jumpcloud_application" "test_app" {
  display_name = "test_app_%[1]s"
  sso_url      = "https://sso.jumpcloud.com/saml2/test_app_%[1]s"
}

resource "jumpcloud_user_group" "test_group" {
  name = "test_group_%[1]s"
}

resource "jumpcloud_user_group_association" "test_assoc" {
  group_id  = jumpcloud_user_group.test_group.id
  object_id = jumpcloud_application.test_app.id
  type      = "application"
}
`, name)
}
