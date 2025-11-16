package jumpcloud

import (
	"fmt"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestAccUserGroupMembership(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckUserGroupMembershipDestroy,
		Steps: []resource.TestStep{
			{
				// The only reasonable step is to check if the user is in the state
				// It will be deleted from the state in case the membership could not be
				// established
				Config: testAccUserGroupMembership(rName),
				Check: resource.TestCheckResourceAttrSet("jumpcloud_user_group_membership.test_membership_"+rName,
					"userid"),
			},
		},
	})
}

// This needs to be moved to a group acceptance test later
func testAccUserGroupMembership(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user_%s" {
			username = "%s"
			email = "%s@testorg.com"
		}

		resource "jumpcloud_user_group" "test_group_%s" {
			name = "testgroup_%s"
		}

		resource "jumpcloud_user_group_membership" "test_membership_%s" {
  			userid = "${jumpcloud_user.test_user_%s.id}"
			groupid = "${jumpcloud_user_group.test_group_%s.id}"
  		}
	`, name, name, name, name, name, name, name, name)
}

func testAccCheckUserGroupMembershipDestroy(s *terraform.State) error {
	config := testAccProvider.Meta().(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jumpcloud_user_group_membership" {
			continue
		}

		groupID := rs.Primary.Attributes["groupid"]
		userID := rs.Primary.Attributes["userid"]

		// Check if membership still exists
		isMember, err := checkUserGroupMembership(client, groupID, userID)
		if err != nil {
			return err
		}
		if isMember {
			return fmt.Errorf("user group membership still exists: group %s, user %s", groupID, userID)
		}
	}

	return nil
}
