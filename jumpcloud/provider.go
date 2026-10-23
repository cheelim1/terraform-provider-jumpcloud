package jumpcloud

import (
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// Provider instantiates a terraform provider for Jumpcloud
// This includes all operations on all supported resources and
// global Jumpcloud parameters
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"api_key": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("JUMPCLOUD_API_KEY", nil),
				Description: descriptions["api_key"],
			},
			"org_id": {
				Type:        schema.TypeString,
				Required:    false,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("JUMPCLOUD_ORG_ID", nil),
				Description: descriptions["org_id"],
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"jumpcloud_application":            resourceApplication(),
			"jumpcloud_user":                   resourceUser(),
			"jumpcloud_user_group":             resourceUserGroup(),
			"jumpcloud_user_group_membership":  resourceUserGroupMembership(),
			"jumpcloud_system_group":           resourceGroupsSystem(),
			"jumpcloud_user_group_association": resourceUserGroupAssociation(),
			"jumpcloud_organization_settings":  resourceOrganizationSettings(),

			// JumpCloud's MFA model spans three layers, each covered by its own
			// resource(s) below. None of the endpoints behind them are documented
			// in jcapi-go (this provider's Go SDK, no real code change since
			// 2019) or jc-cli (JumpCloud's own actively maintained CLI) -- all
			// were built from captured browser requests against JumpCloud's
			// console. jc-cli's own code claims "JumpCloud exposes no public MFA
			// configuration endpoint" at all; that claim describes the limits of
			// jc-cli's own OpenAPI spec coverage, not the live API -- its own
			// coverage tracking explicitly excludes "console-internal" endpoints
			// from its command surface, and every resource below is evidently
			// one of them.
			//
			//  1. Enrollment policy -- org-wide, and foundational: whether every
			//     user must enroll an MFA factor at all, and the grace period
			//     before that enforcement begins. Nothing below matters until
			//     users actually have a factor enrolled.
			//       jumpcloud_mfa_enrollment_policy
			//
			//  2. Factor availability -- org-wide: which factor types (TOTP,
			//     WebAuthn, Push, JumpCloud Go, ...) exist as enrollable options
			//     at all for this org (jumpcloud_mfa_factor), plus each factor's
			//     own "Additional Settings" config object, each with a different
			//     shape and write pattern: WebAuthn accepts a minimal partial PUT
			//     (jumpcloud_webauthn_settings); Push requires the full object
			//     back, including its server-controlled readOnly flag
			//     (jumpcloud_push_settings); JumpCloud Go (DURT) is a flat
			//     singleton with no id, like the enrollment policy above
			//     (jumpcloud_durt_settings); TOTP has no settings/config object
			//     of its own at all -- jumpcloud_mfa_factor is the entire story
			//     for it, and whether it even appears as a standalone factor is
			//     controlled by jumpcloud_push_settings' is_totp_integrated
			//     field ("combined" vs "separate" in the console UI). A factor
			//     type must be enabled via jumpcloud_mfa_factor before any
			//     policy below can reference it in its MFA requirement.
			//       jumpcloud_mfa_factor, jumpcloud_webauthn_settings,
			//       jumpcloud_push_settings, jumpcloud_durt_settings
			//
			//  3. Authentication policies -- scoped: conditional-access rules
			//     that actually grant/deny access and that layer an MFA
			//     requirement on top, each targeting a specific resource type
			//     (user portal, admin portal, ...), application, or user group,
			//     and each naming which of the org's enabled factor types (from
			//     layer 2) satisfy its requirement. A user can be covered by
			//     several of these at different scopes at once -- e.g. a broad
			//     policy requiring MFA for all user-portal logins, plus a
			//     narrower one layering a stricter factor requirement onto one
			//     sensitive application.
			//       jumpcloud_authentication_policy
			//
			//  4. Default access policies -- one fallback policy per resource
			//     type (application, userportal, adminportal, ldap), distinct
			//     from the named/targeted policies in layer 3. Each is a true
			//     singleton identified entirely by its URL, with a simpler
			//     effect object (no factor-type restriction was observed in a
			//     captured write).
			//       jumpcloud_default_access_policy
			"jumpcloud_mfa_enrollment_policy": resourceMfaEnrollmentPolicy(),
			"jumpcloud_mfa_factor":            resourceMfaFactor(),
			"jumpcloud_webauthn_settings":     resourceWebauthnSettings(),
			"jumpcloud_push_settings":         resourcePushSettings(),
			"jumpcloud_durt_settings":         resourceDurtSettings(),
			"jumpcloud_authentication_policy": resourceAuthenticationPolicy(),
			"jumpcloud_default_access_policy": resourceDefaultAccessPolicy(),
		},
		DataSourcesMap: map[string]*schema.Resource{
			"jumpcloud_user":        dataSourceJumpCloudUser(),
			"jumpcloud_user_group":  dataSourceJumpCloudUserGroup(),
			"jumpcloud_application": dataSourceJumpCloudApplication(),
		},
		ConfigureFunc: providerConfigure,
	}
}

var descriptions map[string]string

func init() {
	descriptions = map[string]string{
		"api_key": "The x-api-key header used to connect to JumpCloud.",
		"org_id":  "The x-org-id header used to connect to JumpCloud.",
	}
}

func providerConfigure(d *schema.ResourceData) (interface{}, error) {
	config := Config{
		APIKey: d.Get("api_key").(string),
		OrgID:  d.Get("org_id").(string),
	}

	return config.Client()
}
