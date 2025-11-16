package jumpcloud

import (
	"context"
	"fmt"
	"testing"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestAccApplication(t *testing.T) {
	randSuffix := acctest.RandStringFromCharSet(10, acctest.CharSetAlpha)
	fullResourceName := "jumpcloud_application.example_app"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckApplicationDestroy,
		Steps: []resource.TestStep{
			// Create step
			{
				Config: testApplicationConfig(randSuffix, "test_aws_account", "test_attribute_value"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "display_label", "test_aws_account"),
					resource.TestCheckResourceAttr(fullResourceName, "name", "test-app-"+randSuffix),
					resource.TestCheckResourceAttr(fullResourceName, "sso_url", "https://sso.jumpcloud.com/saml2/example-application_"+randSuffix),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.name", "test_attribute_name"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.value", "test_attribute_value"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.read_only", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.required", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.visible", "true"),
				),
			},
			applicationImportStep(fullResourceName),
			// Update Step
			{
				Config: testApplicationConfig(randSuffix, "test_aws_account_updated", "updated_test_value"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fullResourceName, "display_label", "test_aws_account_updated"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.name", "test_attribute_name"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.value", "updated_test_value"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.read_only", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.required", "false"),
					resource.TestCheckResourceAttr(fullResourceName, "constant_attributes.0.visible", "true"),
				),
			},
			applicationImportStep(fullResourceName),
		},
	})
}

// testApplicationConfig generates the Terraform configuration for testing
func testApplicationConfig(randSuffix string, displayLabel string, constantAttrValue string) string {
	// Using dummy/test values for SAML configuration fields
	// In real tests, these would be valid SAML configuration values
	return fmt.Sprintf(`
resource "jumpcloud_application" "example_app" {
	name         = "test-app-%s"
	display_label = "%s"
	sso_url      = "https://sso.jumpcloud.com/saml2/example-application_%s"
	idp_certificate = "-----BEGIN CERTIFICATE-----\nTEST_CERTIFICATE\n-----END CERTIFICATE-----"
	idp_entity_id  = "https://test-idp.example.com"
	idp_private_key = "-----BEGIN PRIVATE KEY-----\nTEST_PRIVATE_KEY\n-----END PRIVATE KEY-----"
	sp_entity_id   = "https://test-sp.example.com"
	acs_url        = "https://test-sp.example.com/acs"

	constant_attributes {
		name      = "test_attribute_name"
		value     = "%s"
		read_only = false
		required  = false
		visible   = true
	}
}
`, randSuffix, displayLabel, randSuffix, constantAttrValue)
}

// applicationImportStep is used to test resource import functionality
func applicationImportStep(resourceName string) resource.TestStep {
	return resource.TestStep{
		ResourceName:      resourceName,
		ImportState:       true,
		ImportStateVerify: true,
	}
}

func testAccCheckApplicationDestroy(s *terraform.State) error {
	configv1 := convertV2toV1Config(testAccProvider.Meta().(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jumpcloud_application" {
			continue
		}

		_, _, err := client.ApplicationsApi.ApplicationsGet(context.TODO(), rs.Primary.ID, nil)
		if err == nil {
			return fmt.Errorf("application still exists: %s", rs.Primary.ID)
		}
		// EOF error means the resource doesn't exist, which is what we want
		if err.Error() != "EOF" {
			return err
		}
	}

	return nil
}
