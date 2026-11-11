package jumpcloud

// Drives real Terraform core (in-process, via SDK v1's helper/resource)
// through the jumpcloud_bookmark_application lifecycle against the stateful
// fake JumpCloud v1 applications API from application_lifecycle_test.go.
// Hermetic: no network, no TF_ACC.

import (
	"fmt"
	"net/http/httptest"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

const bookmarkConfig = `
provider "jumpcloud" { api_key = "k" }
resource "jumpcloud_bookmark_application" "b" {
  display_label = "%s"
  url           = "%s"
  description   = "%s"
  active        = %s
}
`

// The request body the JumpCloud console sends when an admin adds a bookmark
// (captured from a browser), minus the fields the test varies.
func consoleBookmarkBody() map[string]interface{} {
	return map[string]interface{}{
		"active":                    true,
		"beta":                      false,
		"color":                     "#006CAC",
		"config":                    map[string]interface{}{},
		"databaseAttributes":        []interface{}{},
		"description":               "This is a test bookmark app.",
		"displayLabel":              "Test Bookmark",
		"displayName":               "Bookmark",
		"id":                        "",
		"isBookmark":                true,
		"isCustomLogo":              false,
		"keywords":                  "",
		"learnMore":                 "",
		"logo":                      map[string]interface{}{"color": "#006CAC"},
		"name":                      "bookmark",
		"provision":                 nil,
		"sso":                       map[string]interface{}{"beta": false, "hidden": false, "jit": false, "spErrorFlow": false, "type": "bookmark"},
		"ssoUrl":                    "https://www.google.com/",
		"type":                      "application",
		"scimPrimaryOrganizationId": "",
		"enabledCapabilities":       []interface{}{"bookmark"},
		"ssoDirName":                "",
		"ssoFileName":               "",
	}
}

func TestBookmarkApplicationLifecycle(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	// The live API accepts isBookmark on a write but does not return it.
	f := &fakeApplications{apps: map[string]map[string]interface{}{}, dropOnStore: []string{"isBookmark"}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		CheckDestroy: func(*terraform.State) error {
			if f.app() != nil {
				return fmt.Errorf("bookmark application not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// Create sends exactly what the console sends.
				Config: fmt.Sprintf(bookmarkConfig, "Test Bookmark", "https://www.google.com/", "This is a test bookmark app.", "true"),
				Check: resource.ComposeTestCheckFunc(
					func(*terraform.State) error {
						got := f.lastBody()
						want := consoleBookmarkBody()
						if !reflect.DeepEqual(got, want) {
							return fmt.Errorf("create body differs from the console's:\n got  %v\n want %v", got, want)
						}
						return nil
					},
					resource.TestCheckResourceAttr("jumpcloud_bookmark_application.b", "display_label", "Test Bookmark"),
					resource.TestCheckResourceAttr("jumpcloud_bookmark_application.b", "url", "https://www.google.com/"),
					resource.TestCheckResourceAttr("jumpcloud_bookmark_application.b", "color", "#006CAC"),
					resource.TestCheckResourceAttr("jumpcloud_bookmark_application.b", "active", "true"),
				),
			},
			{
				ResourceName:      "jumpcloud_bookmark_application.b",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// A console-side change to a field Terraform doesn't manage must
				// survive an update.
				PreConfig: func() {
					f.mu.Lock()
					for _, a := range f.apps {
						a["keywords"] = "set-in-console"
					}
					f.mu.Unlock()
				},
				Config: fmt.Sprintf(bookmarkConfig, "Claude Platform", "https://platform.claude.com/login?sso=true", "", "false"),
				Check: func(*terraform.State) error {
					a := f.app()
					if a["displayLabel"] != "Claude Platform" || a["ssoUrl"] != "https://platform.claude.com/login?sso=true" ||
						a["description"] != "" || a["active"] != false {
						return fmt.Errorf("update not applied: %v", a)
					}
					if a["keywords"] != "set-in-console" {
						return fmt.Errorf("unmanaged field was reset by an update: keywords=%v", a["keywords"])
					}
					if sso, _ := a["sso"].(map[string]interface{}); sso["type"] != "bookmark" || a["name"] != "bookmark" {
						return fmt.Errorf("update lost the bookmark identity: %v", a)
					}
					// GET does not return isBookmark, so the update's seed lacks it;
					// the update must still send it, as the console does.
					if f.lastBody()["isBookmark"] != true {
						return fmt.Errorf("update did not send isBookmark: %v", f.lastBody())
					}
					return nil
				},
			},
			{
				// No drift after the update.
				Config:   fmt.Sprintf(bookmarkConfig, "Claude Platform", "https://platform.claude.com/login?sso=true", "", "false"),
				PlanOnly: true,
			},
		},
	})
}

// Importing the ID of a SAML application must fail, not adopt it as a bookmark
// with an empty URL that the next apply would overwrite.
func TestBookmarkApplicationImportRejectsNonBookmark(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{
		"saml1": {"_id": "saml1", "name": "saml2", "displayLabel": "Some SAML app", "config": map[string]interface{}{}},
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{
				Config:        fmt.Sprintf(bookmarkConfig, "X", "https://example.com/", "", "true"),
				ResourceName:  "jumpcloud_bookmark_application.b",
				ImportState:   true,
				ImportStateId: "saml1",
				ExpectError:   regexp.MustCompile("is not a bookmark application"),
			},
		},
	})
}

// A bookmark deleted in the console is recreated rather than failing the refresh.
func TestBookmarkApplicationDeletedOutOfBandIsRecreated(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := &fakeApplications{apps: map[string]map[string]interface{}{}, dropOnStore: []string{"isBookmark"}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	cfg := fmt.Sprintf(bookmarkConfig, "B", "https://example.com/", "", "true")
	resource.UnitTest(t, resource.TestCase{
		Providers: applicationTestProviders(srv.URL),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					f.mu.Lock()
					f.apps = map[string]map[string]interface{}{}
					f.mu.Unlock()
				},
				Config: cfg,
				Check: func(*terraform.State) error {
					if f.app() == nil {
						return fmt.Errorf("bookmark was not recreated")
					}
					return nil
				},
			},
		},
	})
}
