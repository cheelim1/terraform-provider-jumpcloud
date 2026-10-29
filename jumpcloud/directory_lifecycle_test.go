package jumpcloud

// Lifecycle tests for group memberships, associations, system groups, the
// user / user group data sources and member email lookups, against the fake
// directory API (real Terraform core, in-process, no network).

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func membershipConfig(groupID, userID string) string {
	return fmt.Sprintf(`
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_user_group_membership" "m" {
  groupid = %q
  userid  = %q
}
`, groupID, userID)
}

// A membership removed outside Terraform is planned for re-adding instead of
// failing every plan, and destroying one that's already gone succeeds.
func TestUserGroupMembershipRemovedOutsideTerraform(t *testing.T) {
	f, srv := newDirectoryServer(t)
	f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "a@example.com"}
	cfg := membershipConfig("g1", "u1")
	removeOutOfBand := func() { f.do(func() { f.members["g1"] = nil }) }
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: cfg},
			{PreConfig: removeOutOfBand, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: cfg, Check: func(*terraform.State) error { // re-added
				if indexOf(f.members["g1"], "u1") < 0 {
					return fmt.Errorf("membership not re-added")
				}
				return nil
			}},
			{PreConfig: removeOutOfBand, Config: `provider "jumpcloud" { api_key = "k" }`}, // destroy when gone
		},
	})
}

// Memberships beyond the first 2,000 members of a group must still be found.
func TestUserGroupMembershipInLargeGroup(t *testing.T) {
	f, srv := newDirectoryServer(t)
	f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "a@example.com"}
	for i := 0; i < 2500; i++ {
		f.members["g1"] = append(f.members["g1"], fmt.Sprintf("bulk%d", i))
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps:     []resource.TestStep{{Config: membershipConfig("g1", "u1")}}, // u1 is member #2501
	})
}

func associationConfig(objectID string) string {
	return fmt.Sprintf(`
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_user_group_association" "a" {
  group_id  = "g1"
  object_id = %q
  type      = "system"
}
`, objectID)
}

// An association beyond the first 100 of its type must still be found (no
// permanent diff), and destroying one that's already gone succeeds.
func TestUserGroupAssociationBeyondFirstPage(t *testing.T) {
	f, srv := newDirectoryServer(t)
	for i := 0; i < 150; i++ {
		f.assocs["g1"] = append(f.assocs["g1"], fakeEdge{id: fmt.Sprintf("sys%d", i), typ: "system"})
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: associationConfig("target")}, // becomes association #151
			{
				PreConfig: func() { f.do(func() { f.assocs["g1"] = f.assocs["g1"][:150] }) },
				Config:    `provider "jumpcloud" { api_key = "k" }`, // destroy when gone
			},
		},
	})
}

const systemGroupConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_system_group" "s" { name = "servers" }
`

func TestSystemGroupDeletedOutsideTerraform(t *testing.T) {
	f, srv := newDirectoryServer(t)
	wipe := func() { f.do(func() { f.systemGroups = map[string]map[string]interface{}{} }) }
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: systemGroupConfig},
			{PreConfig: wipe, Config: systemGroupConfig, PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: systemGroupConfig},
			{PreConfig: wipe, Config: `provider "jumpcloud" { api_key = "k" }`},
		},
	})
}

// The user data source fills in username (it's in the schema but was never
// set), and doesn't turn an API failure into "user not found".
func TestDataSourceUser(t *testing.T) {
	f, srv := newDirectoryServer(t)
	f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "other@example.com", "username": "other"}
	f.users["u2"] = map[string]interface{}{"_id": "u2", "email": "jdoe@example.com", "username": "jdoe"}
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{
				Config: `
provider "jumpcloud" { api_key = "k" }
data "jumpcloud_user" "u" { email = "jdoe@example.com" }
`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jumpcloud_user.u", "id", "u2"),
					resource.TestCheckResourceAttr("data.jumpcloud_user.u", "username", "jdoe"),
				),
			},
			{
				// A quote in the email used to break the hand-built JSON filter,
				// which was then silently dropped -- matching every user.
				Config: `
provider "jumpcloud" { api_key = "k" }
data "jumpcloud_user" "u" { email = "x\"y@example.com" }
`,
				ExpectError: regexp.MustCompile(`no user found with the given email`),
			},
		},
	})
}

// The user group data source must find a group that isn't on the first page
// of groups. Its name filter was passed as the wrong type, so the SDK dropped
// it, and it never paginated.
func TestDataSourceUserGroupInLargeOrg(t *testing.T) {
	f, srv := newDirectoryServer(t)
	for i := 0; i < 250; i++ {
		f.userGroups = append(f.userGroups, map[string]interface{}{"id": fmt.Sprintf("g%d", i), "name": fmt.Sprintf("group-%d", i), "type": "user_group"})
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{{
			Config: `
provider "jumpcloud" { api_key = "k" }
data "jumpcloud_user_group" "g" { group_name = "group-230" }
`,
			Check: resource.TestCheckResourceAttr("data.jumpcloud_user_group.g", "id", "g230"),
		}},
	})
}

// A member email that doesn't belong to any user fails with an error naming
// it, instead of sending an empty user id to the API.
func TestUserGroupMembersUnknownEmail(t *testing.T) {
	f, srv := newDirectoryServer(t)
	f.users["u1"] = map[string]interface{}{"_id": "u1", "email": "a@example.com"}
	_, err := userEmailsToIDs(directoryTestMeta(srv.URL), []interface{}{"a@example.com", "typo@example.com"})
	if err == nil || !regexp.MustCompile(`typo@example.com`).MatchString(err.Error()) {
		t.Fatalf("want an error naming the unknown email, got %v", err)
	}
	ids, err := userEmailsToIDs(directoryTestMeta(srv.URL), []interface{}{"a@example.com"})
	if err != nil || len(ids) != 1 || ids[0] != "u1" {
		t.Fatalf("known email: ids=%v err=%v", ids, err)
	}
	emails, err := userIDsToEmails(directoryTestMeta(srv.URL), []string{"u1", "deleted-user"})
	if err != nil || len(emails) != 1 || emails[0] != "a@example.com" {
		t.Fatalf("ids->emails must skip ids with no user rather than return \"\": %v %v", emails, err)
	}
}
