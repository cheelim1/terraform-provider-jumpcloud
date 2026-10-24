package jumpcloud

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed refresh must not be mistaken for an empty or deleted group. A 5xx
// or auth failure carries a JSON error body, which used to decode into a
// zero-value group: Read then cleared the id, dropping the group from state.
func TestResourceUserGroupReadFailsOnNonNotFoundError(t *testing.T) {
	for name, status := range map[string]int{
		"server error": http.StatusInternalServerError,
		"unauthorized": http.StatusUnauthorized,
		"forbidden":    http.StatusForbidden,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
			}))
			defer server.Close()

			d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})
			d.SetId("g1")

			err := resourceUserGroupRead(d, userGroupTestConfig(server.URL))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "boom")
			assert.Equal(t, "g1", d.Id(), "the group must stay in state when the refresh itself failed")
			assert.Equal(t, "group", d.Get("name"), "state must not be overwritten from an error response")
		})
	}
}

// Create must not track a group under an empty id: it exists in JumpCloud, and
// Terraform would lose it.
func TestResourceUserGroupCreateRequiresId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"group"}`)) // a successful POST response with no id
	}))
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})

	err := resourceUserGroupCreate(d, userGroupTestConfig(server.URL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no id was returned")
	assert.Equal(t, "", d.Id())
}

// A successful GET of /usergroups/{id} is that group, even if the response
// omits the id: Read must not drop it from state.
func TestResourceUserGroupReadKeepsIdWhenResponseOmitsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/usergroups/g1/members" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write([]byte(`{"name":"renamed"}`))
	}))
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})
	d.SetId("g1")

	require.NoError(t, resourceUserGroupRead(d, userGroupTestConfig(server.URL)))
	assert.Equal(t, "g1", d.Id())
	assert.Equal(t, "renamed", d.Get("name"))
}

func TestResourceUserGroupReadNotFoundClearsId(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})
	d.SetId("g1")

	require.NoError(t, resourceUserGroupRead(d, userGroupTestConfig(server.URL)))
	assert.Equal(t, "", d.Id(), "a 404 means the group was deleted outside Terraform")
}

// The group requests used to go through http.DefaultClient with no timeout, so
// a server that accepted the connection and then stalled would hang refresh and
// apply while Terraform holds its state lock.
func TestResourceUserGroupRequestsTimeOutOnStalledServer(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	orig := rawRequestTimeout
	rawRequestTimeout = 100 * time.Millisecond
	defer func() { rawRequestTimeout = orig }()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{"name": "group"})
	d.SetId("g1")

	// Run each call under a deadline so a request with no timeout fails this
	// test promptly instead of hanging it until go test's own limit.
	failsWithin5s := func(name string, fn func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() { done <- fn() }()
		select {
		case err := <-done:
			require.Error(t, err, "a stalled %s must fail rather than succeed", name)
		case <-time.After(5 * time.Second):
			t.Fatalf("a stalled %s did not return within 5s: its request has no timeout", name)
		}
	}
	config := userGroupTestConfig(server.URL)
	failsWithin5s("read", func() error { return resourceUserGroupRead(d, config) })
	failsWithin5s("update", func() error { return resourceUserGroupUpdate(d, config) })
	failsWithin5s("create", func() error { return resourceUserGroupCreate(d, config) })
	assert.Equal(t, "g1", d.Id())
}
