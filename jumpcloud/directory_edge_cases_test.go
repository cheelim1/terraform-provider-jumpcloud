package jumpcloud

// Edge cases the lifecycle tests can't reach through Terraform core: destroy
// against an object that's already gone (core's refresh would drop it from
// state first, so Delete never runs there), a group deleted outside
// Terraform, and the read-back retries after creating a graph edge.

import (
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

func shortenEdgeRetries(t *testing.T, attempts int) {
	t.Helper()
	origN, origD := newEdgeReadRetries, newEdgeReadDelay
	newEdgeReadRetries, newEdgeReadDelay = attempts, 10*time.Millisecond
	t.Cleanup(func() { newEdgeReadRetries, newEdgeReadDelay = origN, origD })
}

// Each resource's Delete, called directly against an object the API no
// longer has, succeeds -- and really did issue the delete.
func TestDeletesTreatNotFoundAsGone(t *testing.T) {
	cases := []struct {
		name   string
		r      *schema.Resource
		id     string
		values map[string]interface{}
		del    func(*schema.ResourceData, interface{}) error
	}{
		{"user", resourceUser(), "gone-user", map[string]interface{}{"username": "x", "email": "x@example.com"}, resourceUserDelete},
		{"membership", resourceUserGroupMembership(), "g1/gone-user", map[string]interface{}{"groupid": "g1", "userid": "gone-user"}, resourceUserGroupMembershipDelete},
		{"association", resourceUserGroupAssociation(), "g1/gone-app/application", map[string]interface{}{"group_id": "g1", "object_id": "gone-app", "type": "application"}, resourceUserGroupAssociationDelete},
		{"system group", resourceGroupsSystem(), "servers", map[string]interface{}{"name": "servers", "jc_id": "gone-sg"}, resourceGroupsSystemDelete},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, srv := newDirectoryServer(t)
			d := schema.TestResourceDataRaw(t, c.r.Schema, c.values)
			d.SetId(c.id)
			if err := c.del(d, directoryTestMeta(srv.URL)); err != nil {
				t.Fatalf("Delete of an already-gone object failed: %v", err)
			}
			if f.deletes != 1 {
				t.Fatalf("expected exactly one delete request, got %d", f.deletes)
			}
		})
	}
}

// When the group itself is deleted outside Terraform, its memberships and
// associations drop out of state (the next apply re-creates them, failing
// there if the group is really gone) instead of failing every refresh.
func TestGroupDeletedOutsideTerraform(t *testing.T) {
	f, srv := newDirectoryServer(t)
	f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "a@example.com"}
	cfg := membershipConfig("g1", "u1") + `
resource "jumpcloud_user_group_association" "a" {
  group_id  = "g1"
  object_id = "app1"
  type      = "application"
}
`
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:          func() { f.do(func() { f.deletedGroups["g1"] = true }) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() { f.do(func() { delete(f.deletedGroups, "g1") }) },
				Config:    cfg,
			},
		},
	})
}

// A just-created membership or association that the graph doesn't list yet
// is retried, not reported as created-then-vanished.
func TestNewEdgesRetriedUntilVisible(t *testing.T) {
	shortenEdgeRetries(t, 5)
	f, srv := newDirectoryServer(t)
	f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "a@example.com"}
	f.hideNewEdgeReads = 3
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{{
			Config: membershipConfig("g1", "u1") + `
resource "jumpcloud_user_group_association" "a" {
  group_id  = "g1"
  object_id = "app1"
  type      = "application"
}
`,
		}},
	})
}

// If a new edge never shows up, Create fails clearly -- with the edge kept
// in state (tainted), so it isn't left in JumpCloud untracked.
func TestNewEdgeNeverVisibleFails(t *testing.T) {
	shortenEdgeRetries(t, 3)
	for _, c := range []struct{ name, cfg, want string }{
		{"membership", membershipConfig("g1", "u1"), `User ID u1 not found in group ID g1 after adding it`},
		{"association", associationConfig("app1"), `association .* not found after adding it`},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, srv := newDirectoryServer(t)
			f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "a@example.com"}
			f.hideNewEdgeReads = 1000 // never becomes visible
			resource.UnitTest(t, resource.TestCase{
				Providers: directoryTestProviders(srv.URL),
				Steps:     []resource.TestStep{{Config: c.cfg, ExpectError: regexp.MustCompile(c.want)}},
			})
		})
	}
}
