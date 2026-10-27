package jumpcloud

// jumpcloud_user lifecycle against the fake directory API: real Terraform core,
// in-process, no network.

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

const userConfigTmpl = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_user" "u" {
  username = "jdoe"
  email    = "jdoe@example.com"
  %s
}
`

func userConfig(extra string) string { return fmt.Sprintf(userConfigTmpl, extra) }

func wantUserField(f *fakeDirectory, key string, want interface{}) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := f.user(f.onlyUserID())[key]; fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("JumpCloud user %s = %v, want %v", key, got, want)
		}
		return nil
	}
}

// Setting a flag back to false (or a name to a new value) must actually reach
// JumpCloud. The old SDK request struct tags every field omitempty, so false
// was silently never sent: unsuspending, revoking sudo or turning MFA off
// through Terraform did nothing while reporting success.
func TestUserFlagsCanBeTurnedOff(t *testing.T) {
	f, srv := newDirectoryServer(t)
	on := `
  sudo                   = true
  suspended              = true
  enable_mfa             = true
  ldap_binding_user      = true
  password_never_expires = true
  passwordless_sudo      = true`
	off := `
  sudo                   = false
  suspended              = false
  enable_mfa             = false
  ldap_binding_user      = false
  password_never_expires = false
  passwordless_sudo      = false`
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{
				Config: userConfig(on),
				Check: resource.ComposeTestCheckFunc(
					wantUserField(f, "sudo", true), wantUserField(f, "suspended", true),
					wantUserField(f, "enable_user_portal_multifactor", true),
					wantUserField(f, "ldap_binding_user", true), wantUserField(f, "password_never_expires", true),
					wantUserField(f, "passwordless_sudo", true),
				),
			},
			{
				Config: userConfig(off),
				Check: resource.ComposeTestCheckFunc(
					wantUserField(f, "sudo", false), wantUserField(f, "suspended", false),
					wantUserField(f, "enable_user_portal_multifactor", false),
					wantUserField(f, "ldap_binding_user", false), wantUserField(f, "password_never_expires", false),
					wantUserField(f, "passwordless_sudo", false),
				),
			},
			{ // and back on again
				Config: userConfig(on),
				Check:  resource.ComposeTestCheckFunc(wantUserField(f, "sudo", true), wantUserField(f, "passwordless_sudo", true)),
			},
		},
	})
}

// The password is only sent when it changes. Re-sending it on every update
// re-submits it as a "new" password, which JumpCloud's password-history policy
// can reject, failing unrelated changes like a rename.
func TestUserPasswordOnlySentWhenChanged(t *testing.T) {
	f, srv := newDirectoryServer(t)
	sentPassword := func(want bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			var last map[string]interface{}
			f.do(func() { last = f.userPuts[len(f.userPuts)-1] })
			if _, sent := last["password"]; sent != want {
				return fmt.Errorf("password in last update = %v, want %v (body %v)", sent, want, last)
			}
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: userConfig(`password = "Initial-Pass-1"` + "\n firstname = \"Jane\"")},
			{Config: userConfig(`password = "Initial-Pass-1"` + "\n firstname = \"Janet\""), Check: sentPassword(false)},
			{Config: userConfig(`password = "Rotated-Pass-2"` + "\n firstname = \"Janet\""), Check: sentPassword(true)},
		},
	})
}

// A setting changed in the console on an attribute the config doesn't set
// must not show as a change, and an unrelated apply must leave it alone. With
// the old schema the plan showed e.g. suspended true -> false forever; once
// false can be sent, applying that would unsuspend an offboarded user.
func TestUserUnmanagedConsoleSettingsAreLeftAlone(t *testing.T) {
	f, srv := newDirectoryServer(t)
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: userConfig(`firstname = "Jane"`)},
			{
				PreConfig: func() {
					f.do(func() {
						for _, u := range f.users {
							u["suspended"] = true // offboarded in the console
							u["sudo"] = true
							u["lastname"] = "Doe"
							u["phoneNumbers"] = []interface{}{map[string]interface{}{"type": "mobile", "number": "+60123"}}
						}
					})
				},
				Config:   userConfig(`firstname = "Jane"`),
				PlanOnly: true, // must be empty
			},
			{
				Config: userConfig(`firstname = "Janet"`),
				Check: resource.ComposeTestCheckFunc(
					wantUserField(f, "firstname", "Janet"),
					wantUserField(f, "suspended", true),
					wantUserField(f, "sudo", true),
					wantUserField(f, "lastname", "Doe"),
				),
			},
		},
	})
}

// A user deleted outside Terraform is planned for re-creation, and destroying
// one that's already gone succeeds, instead of every plan/destroy failing.
func TestUserDeletedOutsideTerraform(t *testing.T) {
	f, srv := newDirectoryServer(t)
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: userConfig("")},
			{
				PreConfig:          func() { f.do(func() { f.users = map[string]map[string]interface{}{} }) },
				Config:             userConfig(""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: userConfig("")}, // re-created
			{
				// destroy after an out-of-band delete
				PreConfig: func() { f.do(func() { f.users = map[string]map[string]interface{}{} }) },
				Config:    `provider "jumpcloud" { api_key = "k" }`,
			},
		},
	})
}

func TestUserPhoneNumbersAndNames(t *testing.T) {
	f, srv := newDirectoryServer(t)
	resource.UnitTest(t, resource.TestCase{
		Providers: directoryTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{
				Config: userConfig(`
  firstname    = "Jane"
  display_name = "JD"
  phone_number {
    type   = "mobile"
    number = "+60111"
  }`),
				Check: func(*terraform.State) error {
					u := f.user(f.onlyUserID())
					if u["displayname"] != "JD" || len(u["phoneNumbers"].([]interface{})) != 1 {
						return fmt.Errorf("create body wrong: %v", u)
					}
					return nil
				},
			},
			{
				Config: userConfig(`
  firstname    = "Jane"
  display_name = "Jane D"
  phone_number {
    type   = "work"
    number = "+60222"
  }`),
				Check: func(*terraform.State) error {
					u := f.user(f.onlyUserID())
					pn := u["phoneNumbers"].([]interface{})
					if u["displayname"] != "Jane D" || len(pn) != 1 || pn[0].(map[string]interface{})["number"] != "+60222" {
						return fmt.Errorf("update not applied: %v", u)
					}
					return nil
				},
			},
			{
				Config:      userConfig("phone_number {\n type = \"pager\"\n number = \"1\"\n }"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`expected phone_number.0.type to be one of`),
			},
			{
				ResourceName: "jumpcloud_user.u", ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"password"},
				Config: userConfig(`
  firstname    = "Jane"
  display_name = "Jane D"
  phone_number {
    type   = "work"
    number = "+60222"
  }`),
			},
		},
	})
}
