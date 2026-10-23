package jumpcloud

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applicationTestValues() map[string]interface{} {
	return map[string]interface{}{
		"name": "aws", "display_label": "AWS", "sso_url": "aws-sso",
		"idp_entity_id": "jc-idp", "sp_entity_id": "urn:amazon:webservices", "acs_url": "https://example.com/acs",
		"idp_private_key": "SUPER-SECRET-PRIVATE-KEY",
	}
}

// Create must never write idp_private_key (or the request body carrying it)
// to the provider log, which TF_LOG exposes.
func TestResourceApplicationCreateDoesNotLogPrivateKey(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"_id": "app1", "name": "aws"})
	}))
	defer server.Close()
	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL + "/v2"
	config.AddDefaultHeader("x-api-key", "test-key")
	config.AddDefaultHeader("x-org-id", "") // metadata fetch failure is then only a warning

	d := schema.TestResourceDataRaw(t, resourceApplication().Schema, applicationTestValues())
	require.NoError(t, resourceApplicationCreate(d, config))
	assert.Equal(t, "app1", d.Id())
	assert.NotContains(t, logs.String(), "SUPER-SECRET-PRIVATE-KEY")
}

// A create response without an _id must fail loudly rather than leave an
// application in JumpCloud that Terraform doesn't track.
func TestResourceApplicationCreateRequiresId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"name": "aws"})
	}))
	defer server.Close()
	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL + "/v2"
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceApplication().Schema, applicationTestValues())
	err := resourceApplicationCreate(d, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no _id")
}

// A successful GET that omits _id must not drop the application from state.
func TestResourceApplicationReadKeepsIdWhenResponseOmitsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"name": "aws"})
	}))
	defer server.Close()
	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL + "/v2"
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceApplication().Schema, applicationTestValues())
	d.SetId("app1")
	require.NoError(t, resourceApplicationRead(d, config))
	assert.Equal(t, "app1", d.Id())
}

func TestResourceApplicationReadNotFoundClearsId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL + "/v2"
	config.AddDefaultHeader("x-api-key", "test-key")

	d := schema.TestResourceDataRaw(t, resourceApplication().Schema, applicationTestValues())
	d.SetId("app1")
	require.NoError(t, resourceApplicationRead(d, config))
	assert.Equal(t, "", d.Id())
}
