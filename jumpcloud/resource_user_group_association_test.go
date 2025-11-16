package jumpcloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestAccUserGroupAssociation(t *testing.T) {
	randSuffix := acctest.RandString(10)
	resourceName := "jumpcloud_user_group_association.test_association"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckUserGroupAssociationDestroy,
		Steps: []resource.TestStep{
			{
				Config: testUserGroupAssocConfig(randSuffix),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "group_id"),
					resource.TestCheckResourceAttrSet(resourceName, "object_id"),
					resource.TestCheckResourceAttrSet(resourceName, "type"),
				),
			},
			{
				// Test Import Functionality
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					// Retrieve the resource from the state
					rs, ok := s.RootModule().Resources[resourceName]
					if !ok {
						return "", fmt.Errorf("Not found: %s", resourceName)
					}
					groupID := rs.Primary.Attributes["group_id"]
					objectID := rs.Primary.Attributes["object_id"]
					objectType := rs.Primary.Attributes["type"]

					return fmt.Sprintf("%s/%s/%s", groupID, objectID, objectType), nil
				},
			},
		},
	})
}

func testUserGroupAssocConfig(randSuffix string) string {
	return fmt.Sprintf(`
resource "jumpcloud_application" "test_application" {
  name            = "test-app-%s"
  display_name    = "test_application_%s"
  sso_url         = "https://sso.jumpcloud.com/saml2/example-application-%s"
  idp_certificate = "-----BEGIN CERTIFICATE-----\nTEST_CERTIFICATE\n-----END CERTIFICATE-----"
  idp_entity_id   = "https://test-idp.example.com"
  idp_private_key  = "-----BEGIN PRIVATE KEY-----\nTEST_PRIVATE_KEY\n-----END PRIVATE KEY-----"
  sp_entity_id     = "https://test-sp.example.com"
  acs_url          = "https://test-sp.example.com/acs"
}

resource "jumpcloud_user_group" "test_group" {
  name = "testgroup_%s"
}

resource "jumpcloud_user_group_association" "test_association" {
  object_id = jumpcloud_application.test_application.id
  group_id  = jumpcloud_user_group.test_group.id
  type      = "application"
}
`, randSuffix, randSuffix, randSuffix, randSuffix)
}

// CheckDestroy function to ensure the resource is properly destroyed.
func testAccCheckUserGroupAssociationDestroy(s *terraform.State) error {
	// Since we're not using the JumpCloud Go SDK v2, we'll use HTTP requests.
	apiKey := os.Getenv("JUMPCLOUD_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("JUMPCLOUD_API_KEY must be set for acceptance tests")
	}

	client := &http.Client{}
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jumpcloud_user_group_association" {
			continue
		}

		groupID := rs.Primary.Attributes["group_id"]
		objectID := rs.Primary.Attributes["object_id"]
		objectType := rs.Primary.Attributes["type"]

		// Build the request URL.
		url := fmt.Sprintf("https://console.jumpcloud.com/api/v2/usergroups/%s/%s", groupID, objectType)

		// Prepare the request.
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Add("x-api-key", apiKey)
		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("Accept", "application/json")

		// Execute the request.
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Check for non-200 status codes.
		if resp.StatusCode != 200 {
			// If the resource is not found, it's successfully destroyed.
			if resp.StatusCode == 404 {
				continue
			}
			return fmt.Errorf("Failed to verify destruction of user group association: %s", resp.Status)
		}

		// Parse the response to check if the association still exists.
		var associations []map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&associations)
		if err != nil {
			return err
		}

		for _, association := range associations {
			if association["id"] == objectID {
				return fmt.Errorf("User group association still exists: %s/%s/%s", groupID, objectID, objectType)
			}
		}
	}

	return nil
}
