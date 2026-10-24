package jumpcloud

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A group that was already deleted outside Terraform is the desired end state
// of a destroy: a 404 must not fail it (and so block it forever).
func TestResourceUserGroupDeleteNotFoundIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})
	d.SetId("g1")

	require.NoError(t, resourceUserGroupDelete(d, userGroupTestConfig(server.URL)))
	assert.Equal(t, "", d.Id())
}

// Any other failure must still fail the destroy and keep the group tracked.
func TestResourceUserGroupDeleteStillFailsOnOtherErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})
	d.SetId("g1")

	require.Error(t, resourceUserGroupDelete(d, userGroupTestConfig(server.URL)))
	assert.Equal(t, "g1", d.Id())
}
