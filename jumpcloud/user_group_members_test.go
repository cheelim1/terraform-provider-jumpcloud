package jumpcloud

// jumpcloud_user_group members (email <-> id mapping) through real Terraform
// core against the fake directory API.

import (
	"fmt"
	"regexp"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func userGroupMembersConfig(members string) string {
	return fmt.Sprintf(`
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_user_group" "g" {
  name    = "devs"
  members = [%s]
}
`, members)
}

func TestUserGroupMembers(t *testing.T) {
	f, srv := newDirectoryServer(t)
	for _, u := range []string{"a", "b", "c"} {
		f.users["id-"+u] = map[string]interface{}{"_id": "id-" + u, "email": u + "@example.com", "username": u}
	}
	wantMembers := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			var got []string
			f.do(func() {
				for _, ids := range f.members {
					got = append(got, ids...)
				}
			})
			sort.Strings(got)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				return fmt.Errorf("members = %v, want %v", got, want)
			}
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: userGroupMembersConfig(`"a@example.com", "b@example.com"`), Check: wantMembers("id-a", "id-b")},
			// order doesn't matter
			{Config: userGroupMembersConfig(`"b@example.com", "a@example.com"`), PlanOnly: true},
			{Config: userGroupMembersConfig(`"a@example.com", "c@example.com"`), Check: wantMembers("id-a", "id-c")},
			// a member removed in the console is re-added
			{
				PreConfig: func() { f.do(func() { f.members = map[string][]string{} }) },
				Config:    userGroupMembersConfig(`"a@example.com", "c@example.com"`),
				Check:     wantMembers("id-a", "id-c"),
			},
			// a mistyped email fails with an error naming it
			{
				Config:      userGroupMembersConfig(`"a@example.com", "typo@example.com"`),
				ExpectError: regexp.MustCompile(`no JumpCloud user found for member email\(s\): typo@example.com`),
			},
			{Config: userGroupMembersConfig(`"a@example.com"`), Check: wantMembers("id-a")},
		},
	})
}
