package jumpcloud

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestProviderInitialization(t *testing.T) {
	if err := Provider().InternalValidate(); err != nil {
		t.Fatalf("err: %s", err)
	}
}

func TestProviderImplementation(t *testing.T) {
	var _ *schema.Provider = Provider()
}

var testAccProviders map[string]terraform.ResourceProvider
var testAccProvider *schema.Provider

func init() {
	testAccProvider = Provider()
	testAccProviders = map[string]terraform.ResourceProvider{
		"jumpcloud": testAccProvider,
	}
}

func testAccPreCheck(t *testing.T) {
	if v := os.Getenv("JUMPCLOUD_API_KEY"); v == "" {
		t.Fatal("JUMPCLOUD_API_KEY must be set for acceptance tests")
	}
}
