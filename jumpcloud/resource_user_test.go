package jumpcloud

import (
	"context"
	"fmt"
	"os"
	"testing"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestAccUser(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				// This test simply applys a user with the config from testAccUser
				// and checks for the correct username and email in the state
				// The resource is destroyed afterwards via the framework
				Config: testAccUser(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "email", rName+"@testorg.com"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "ldap_binding_user", "false"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "password_never_expires", "false"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "sudo", "false"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "suspended", "false"),
				),
			},
		},
	})
}

func TestAccUserFull(t *testing.T) {
	rName := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckUserDestroy,
		Steps: []resource.TestStep{
			{
				// This test simply applys a user with the config from testAccUser
				// and checks for the correct username and email in the state
				// The resource is destroyed afterwards via the framework
				Config: testAccUserFull(rName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "username", rName),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "email", rName+"@testorg.com"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "ldap_binding_user", "true"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "password_never_expires", "true"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "sudo", "true"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "suspended", "true"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "phone_number.0.type", "work"),
					resource.TestCheckResourceAttr("jumpcloud_user.test_user", "phone_number.0.number", "855.212.3122"),
				),
			},
		},
	})
}

// testAccPreCheck validates the necessary test API keys exist
// in the testing environment
func testAccPreCheck(t *testing.T) {
	if v := os.Getenv("JUMPCLOUD_API_KEY"); v == "" {
		t.Fatal("JUMPCLOUD_API_KEY= must be set for the acceptance tests")
	}
}

func testAccUser(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
  			username = "%s"
			email = "%s@testorg.com"
			firstname = "Firstname"
			lastname = "Lastname"
			enable_mfa = true
		}`, name, name,
	)
}

func testAccUserFull(name string) string {
	return fmt.Sprintf(`
		resource "jumpcloud_user" "test_user" {
  			username = "%s"
			email = "%s@testorg.com"
			firstname = "Firstname"
			lastname = "Lastname"
			enable_mfa = true
			ldap_binding_user = true
			password_never_expires = true
			sudo = true
			suspended = true
            phone_number {
                type = "work"
				number = "855.212.3122"
			}
		}`, name, name,
	)
}

func testAccCheckUserDestroy(s *terraform.State) error {
	configv1 := convertV2toV1Config(testAccProvider.Meta().(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jumpcloud_user" {
			continue
		}

		_, _, err := client.SystemusersApi.SystemusersGet(context.TODO(), rs.Primary.ID, "", "", nil)
		if err == nil {
			return fmt.Errorf("user still exists: %s", rs.Primary.ID)
		}
		// EOF error means the resource doesn't exist, which is what we want
		if err.Error() != "EOF" {
			return err
		}
	}

	return nil
}
