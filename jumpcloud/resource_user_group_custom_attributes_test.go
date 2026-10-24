package jumpcloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userGroupTestServer fakes the user group endpoints these tests touch. The
// group starts as `group`; a PATCH or POST body is recorded in *got and
// becomes the group's new state, so the Read that follows sees it.
func userGroupTestServer(t *testing.T, group map[string]interface{}, got *map[string]interface{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		assert.Equal(t, "test-org", r.Header.Get("x-org-id"), "%s %s must carry x-org-id", r.Method, r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/usergroups/g1":
			_ = json.NewEncoder(w).Encode(group)
		case r.Method == http.MethodGet && r.URL.Path == "/usergroups/g1/members":
			_, _ = w.Write([]byte("[]"))
		case (r.Method == http.MethodPatch && r.URL.Path == "/usergroups/g1") ||
			(r.Method == http.MethodPost && r.URL.Path == "/usergroups"):
			require.NoError(t, json.NewDecoder(r.Body).Decode(got))
			group = map[string]interface{}{}
			for k, v := range *got {
				group[k] = v
			}
			group["id"] = "g1"
			_ = json.NewEncoder(w).Encode(group)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
}

func userGroupTestConfig(url string) *jcapiv2.Configuration {
	config := jcapiv2.NewConfiguration()
	config.BasePath = url
	config.AddDefaultHeader("x-api-key", "test-key")
	config.AddDefaultHeader("x-org-id", "test-org")
	return config
}

// JumpCloud's PATCH /usergroups/{id} behaves like a PUT -- confirmed live,
// it clears any top-level field (e.g. description) or attributes key (e.g. a
// custom attribute) the request omits. Update must therefore send back the
// group's full current state with only the managed fields changed.
func TestResourceUserGroupUpdatePreservesUnmanagedFields(t *testing.T) {
	var got map[string]interface{}
	server := userGroupTestServer(t, map[string]interface{}{
		"id":                   "g1",
		"type":                 "user_group",
		"organizationObjectId": "org1",
		"name":                 "old-name",
		"description":          "set in the console",
		"email":                "",
		"membershipMethod":     "STATIC",
		"attributes": map[string]interface{}{
			"ldapGroups":  []interface{}{map[string]interface{}{"name": "old-name"}},
			"posixGroups": []interface{}{map[string]interface{}{"id": float64(4321), "name": "posix"}},
			"jsonAttr":    map[string]interface{}{"nested": "value"},
		},
	}, &got)
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{
		"name": "new-name",
		"custom_attributes": map[string]interface{}{
			"snowflakeDefaultRole": "SYSADMIN",
		},
	})
	d.SetId("g1")

	require.NoError(t, resourceUserGroupUpdate(d, userGroupTestConfig(server.URL)))

	assert.Equal(t, "new-name", got["name"])
	assert.Equal(t, "set in the console", got["description"],
		"an unmanaged top-level field must survive the update")
	assert.Equal(t, "STATIC", got["membershipMethod"])
	for _, k := range []string{"id", "type", "organizationObjectId"} {
		assert.NotContains(t, got, k, "read-only field %q should not be sent back", k)
	}

	attrs := got["attributes"].(map[string]interface{})
	assert.Equal(t, "SYSADMIN", attrs["snowflakeDefaultRole"])
	assert.Equal(t, []interface{}{map[string]interface{}{"id": float64(4321), "name": "posix"}}, attrs["posixGroups"],
		"posix groups must survive an update that doesn't configure posix_groups")
	assert.Equal(t, map[string]interface{}{"nested": "value"}, attrs["jsonAttr"],
		"a non-string custom attribute (not representable here) must be left alone")
	assert.Contains(t, attrs, "ldapGroups")

	assert.Equal(t, map[string]interface{}{"snowflakeDefaultRole": "SYSADMIN"}, d.Get("custom_attributes"))
}

func TestApplyCustomAttributes(t *testing.T) {
	attrs := map[string]interface{}{
		"ldapGroups": []interface{}{},
		"keep":       "unchanged",
		"change":     "old",
		"remove":     "gone",
	}
	applyCustomAttributes(attrs,
		map[string]interface{}{"keep": "unchanged", "change": "old", "remove": "gone"},
		map[string]interface{}{"keep": "unchanged", "change": "new", "add": "added"},
	)
	assert.Equal(t, map[string]interface{}{
		"ldapGroups": []interface{}{},
		"keep":       "unchanged",
		"change":     "new",
		"add":        "added",
	}, attrs)
}

func TestApplyCustomAttributesNeverTouchesSystemKeys(t *testing.T) {
	attrs := map[string]interface{}{"posixGroups": []interface{}{"p"}}
	applyCustomAttributes(attrs,
		map[string]interface{}{"posixGroups": "x"},
		map[string]interface{}{"sudo": "y"},
	)
	assert.Equal(t, map[string]interface{}{"posixGroups": []interface{}{"p"}}, attrs)
}

func TestFlattenCustomAttributes(t *testing.T) {
	assert.Equal(t, map[string]interface{}{"snowflakeDefaultRole": "SYSADMIN"}, flattenCustomAttributes(map[string]interface{}{
		"ldapGroups":           []interface{}{},
		"posixGroups":          []interface{}{},
		"sudo":                 map[string]interface{}{"enabled": true},
		"radius":               map[string]interface{}{},
		"sambaEnabled":         false,
		"snowflakeDefaultRole": "SYSADMIN",
		"count":                float64(3),
	}))
	assert.Equal(t, map[string]interface{}{}, flattenCustomAttributes(nil))
}

func TestValidateCustomAttributeKeys(t *testing.T) {
	_, errs := validateCustomAttributeKeys(map[string]interface{}{"snowflakeDefaultRole": "SYSADMIN", "team": "eng"}, "custom_attributes")
	assert.Empty(t, errs)

	_, errs = validateCustomAttributeKeys(map[string]interface{}{}, "custom_attributes")
	assert.Empty(t, errs, "an empty map is valid: it clears the string-valued attributes")

	for _, builtin := range []string{"ldapGroups", "posixGroups", "radius", "sambaEnabled", "sudo"} {
		_, errs = validateCustomAttributeKeys(map[string]interface{}{builtin: "x"}, "custom_attributes")
		require.Len(t, errs, 1, builtin)
		assert.Contains(t, errs[0].Error(), `"`+builtin+`" is a built-in group attribute`)
	}

	_, errs = validateCustomAttributeKeys(map[string]interface{}{"": "x"}, "custom_attributes")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "must not be empty")

	// Every offending key is reported, in a stable order.
	_, errs = validateCustomAttributeKeys(map[string]interface{}{"sudo": "x", "radius": "y"}, "custom_attributes")
	require.Len(t, errs, 2)
	assert.Contains(t, errs[0].Error(), `"radius"`)
	assert.Contains(t, errs[1].Error(), `"sudo"`)
}

func TestResourceUserGroupCreateSendsCustomAttributes(t *testing.T) {
	var got map[string]interface{}
	server := userGroupTestServer(t, nil, &got)
	defer server.Close()

	d := schema.TestResourceDataRaw(t, resourceUserGroup().Schema, map[string]interface{}{
		"name":              "group",
		"attributes":        map[string]interface{}{"posix_groups": "4321:posix"},
		"custom_attributes": map[string]interface{}{"snowflakeDefaultRole": "SYSADMIN"},
	})

	require.NoError(t, resourceUserGroupCreate(d, userGroupTestConfig(server.URL)))

	assert.Equal(t, "g1", d.Id())
	assert.Equal(t, "group", got["name"])
	attrs := got["attributes"].(map[string]interface{})
	assert.Equal(t, "SYSADMIN", attrs["snowflakeDefaultRole"])
	assert.Equal(t, []interface{}{map[string]interface{}{"id": float64(4321), "name": "posix"}}, attrs["posixGroups"])
	assert.Equal(t, map[string]interface{}{"snowflakeDefaultRole": "SYSADMIN"}, d.Get("custom_attributes"))
}
