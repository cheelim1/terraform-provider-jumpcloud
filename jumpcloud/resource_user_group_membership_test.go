package jumpcloud

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
)

func TestUserGroupMembershipResourceBasic(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: nil,
		Steps: []resource.TestStep{
			{
				Config: testUserGroupMembershipResourceConfigBasic(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("jumpcloud_user_group_membership.test_membership", "userid"),
					resource.TestCheckResourceAttrSet("jumpcloud_user_group_membership.test_membership", "groupid"),
				),
			},
		},
	})
}

func testUserGroupMembershipResourceConfigBasic(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
			username = "%[1]s"
			email    = "%[1]s@testorg.com"
		}

		resource "jumpcloud_user_group" "test_group" {
			name = "test_group_%[1]s"
		}

		resource "jumpcloud_user_group_membership" "test_membership" {
			userid  = jumpcloud_user.test_user.id
			groupid = jumpcloud_user_group.test_group.id
		}
	`, name)
}
