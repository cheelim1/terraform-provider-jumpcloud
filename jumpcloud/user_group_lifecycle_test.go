package jumpcloud

// Drives real Terraform core (in-process, via SDK v1's helper/resource)
// through the jumpcloud_user_group lifecycle against a stateful fake of
// JumpCloud's v2 user group API. Hermetic: no network, no TF_ACC.
//
// The fake models the one behavior the resource has to work around: PATCH
// /usergroups/{id} acts like a PUT, so any field or attribute the request omits
// is cleared. A resource that doesn't send back the group's full state loses
// console-set data in this test, exactly as it would against the real API.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

// userGroupTestProviders points the provider at url: v2 calls go to url itself,
// so the fake serves /usergroups/... directly.
func userGroupTestProviders(url string) map[string]terraform.ResourceProvider {
	p := Provider()
	p.ConfigureFunc = func(d *schema.ResourceData) (interface{}, error) {
		c := jcapiv2.NewConfiguration()
		c.BasePath = url
		c.AddDefaultHeader("x-api-key", d.Get("api_key").(string))
		return c, nil
	}
	return map[string]terraform.ResourceProvider{"jumpcloud": p}
}

type fakeUserGroups struct {
	mu     sync.Mutex
	groups map[string]map[string]interface{}
	nextID int
}

func newFakeUserGroups() *fakeUserGroups {
	return &fakeUserGroups{groups: map[string]map[string]interface{}{}}
}

func (f *fakeUserGroups) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/usergroups")
	var body map[string]interface{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	switch {
	case r.Method == http.MethodPost && path == "":
		f.nextID++
		id := fmt.Sprintf("g%d", f.nextID)
		g := deepCopyJSON(body)
		g["id"] = id
		g["type"] = "user_group"
		f.groups[id] = g
		_ = json.NewEncoder(w).Encode(g)
	case strings.HasSuffix(path, "/members") && r.Method == http.MethodGet:
		_, _ = w.Write([]byte("[]"))
	case strings.HasPrefix(path, "/") && !strings.Contains(path[1:], "/"):
		id := path[1:]
		g, ok := f.groups[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(g)
		case http.MethodPatch:
			// Behaves like PUT: the stored group becomes exactly the request.
			g = deepCopyJSON(body)
			g["id"] = id
			g["type"] = "user_group"
			f.groups[id] = g
			_ = json.NewEncoder(w).Encode(g)
		case http.MethodDelete:
			delete(f.groups, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeUserGroups) only() map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.groups {
		return deepCopyJSON(g)
	}
	return nil
}

func (f *fakeUserGroups) edit(fn func(g map[string]interface{})) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.groups {
		fn(g)
	}
}

func (f *fakeUserGroups) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.groups)
}

func newUserGroupFakeServer(t *testing.T) (*fakeUserGroups, *httptest.Server) {
	t.Helper()
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := newFakeUserGroups()
	return f, httptest.NewServer(f)
}

const userGroupLifecycleConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_user_group" "g" {
  name = "%s"
  %s
}
`

func userGroupConfig(name, extra string) string {
	return fmt.Sprintf(userGroupLifecycleConfig, name, extra)
}

// The custom_attributes lifecycle: create, an update that must keep what an
// admin set in the console, drift correction, omitting the argument (leave
// alone), an explicit empty map (clear the string-valued ones), plan-time
// rejection of built-in attribute names, import, and destroy.
func TestUserGroupCustomAttributesLifecycle(t *testing.T) {
	f, srv := newUserGroupFakeServer(t)
	defer srv.Close()

	remoteAttrs := func() map[string]interface{} {
		attrs, _ := f.only()["attributes"].(map[string]interface{})
		return attrs
	}
	check := func(fn func() error) resource.TestCheckFunc {
		return func(*terraform.State) error { return fn() }
	}
	wantAttr := func(key string, want interface{}) resource.TestCheckFunc {
		return check(func() error {
			if got := remoteAttrs()[key]; fmt.Sprint(got) != fmt.Sprint(want) {
				return fmt.Errorf("remote attribute %q = %v, want %v (all: %v)", key, got, want, remoteAttrs())
			}
			return nil
		})
	}
	wantTopLevel := func(key string, want interface{}) resource.TestCheckFunc {
		return check(func() error {
			if got := f.only()[key]; fmt.Sprint(got) != fmt.Sprint(want) {
				return fmt.Errorf("remote %q = %v, want %v", key, got, want)
			}
			return nil
		})
	}
	// consoleEdit is what an admin does in the JumpCloud console to a group
	// Terraform manages: a description and a non-string (JSON) custom attribute,
	// neither of which this resource models.
	consoleEdit := func() {
		f.edit(func(g map[string]interface{}) {
			g["description"] = "set in the console"
			g["attributes"].(map[string]interface{})["jsonAttr"] = map[string]interface{}{"nested": "value"}
		})
	}

	resource.UnitTest(t, resource.TestCase{
		Providers: userGroupTestProviders(srv.URL),
		CheckDestroy: func(*terraform.State) error {
			if n := f.count(); n != 0 {
				return fmt.Errorf("%d group(s) left in JumpCloud after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{ // create; the framework also asserts the follow-up plan is empty
				Config: userGroupConfig("devs", `custom_attributes = { snowflakeDefaultRole = "SYSADMIN" }`),
				Check: resource.ComposeTestCheckFunc(
					wantAttr("snowflakeDefaultRole", "SYSADMIN"),
					resource.TestCheckResourceAttr("jumpcloud_user_group.g", "custom_attributes.snowflakeDefaultRole", "SYSADMIN"),
				),
			},
			{ // update: rename + change the attribute while the console-set data must survive
				PreConfig: consoleEdit,
				Config:    userGroupConfig("devs-renamed", `custom_attributes = { snowflakeDefaultRole = "SECURITYADMIN" }`),
				Check: resource.ComposeTestCheckFunc(
					wantTopLevel("name", "devs-renamed"),
					wantAttr("snowflakeDefaultRole", "SECURITYADMIN"),
					wantTopLevel("description", "set in the console"),
					wantAttr("jsonAttr", map[string]interface{}{"nested": "value"}),
				),
			},
			{ // drift: an out-of-band change to a managed attribute is planned away
				PreConfig: func() {
					f.edit(func(g map[string]interface{}) {
						g["attributes"].(map[string]interface{})["snowflakeDefaultRole"] = "PUBLIC"
					})
				},
				Config: userGroupConfig("devs-renamed", `custom_attributes = { snowflakeDefaultRole = "SECURITYADMIN" }`),
				Check:  wantAttr("snowflakeDefaultRole", "SECURITYADMIN"),
			},
			{ // omitting custom_attributes leaves everything as it is
				Config: userGroupConfig("devs-renamed", ``),
				Check: resource.ComposeTestCheckFunc(
					wantAttr("snowflakeDefaultRole", "SECURITYADMIN"),
					wantTopLevel("description", "set in the console"),
				),
			},
			{ // an explicit empty map clears the string-valued attributes, and only those
				Config: userGroupConfig("devs-renamed", `custom_attributes = {}`),
				Check: resource.ComposeTestCheckFunc(
					check(func() error {
						if _, present := remoteAttrs()["snowflakeDefaultRole"]; present {
							return fmt.Errorf("snowflakeDefaultRole should be cleared, got %v", remoteAttrs())
						}
						return nil
					}),
					wantAttr("jsonAttr", map[string]interface{}{"nested": "value"}),
					wantTopLevel("description", "set in the console"),
				),
			},
			{ // built-in attribute names are rejected at plan time, not silently dropped
				Config:      userGroupConfig("devs-renamed", `custom_attributes = { sudo = "x" }`),
				ExpectError: regexp.MustCompile(`"sudo" is a built-in group attribute`),
			},
			{ // import reproduces what apply recorded
				Config:            userGroupConfig("devs-renamed", `custom_attributes = {}`),
				ResourceName:      "jumpcloud_user_group.g",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
