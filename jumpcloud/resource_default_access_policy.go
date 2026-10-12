package jumpcloud

import (
	"errors"
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// resourceDefaultAccessPolicy manages one of JumpCloud's four fallback access
// policies -- the "Default Access Policy Settings" section of the MFA
// Configurations admin page, distinct from jumpcloud_authentication_policy's
// named, targeted policies. Confirmed via a captured browser request: PUT
// /authn/policy/fallback/{resourceType} (v2), where resourceType is one of
// "application", "userportal", "adminportal", or "ldap" -- each identified
// entirely by its URL, with no id/name of its own.
//
// Body shape confirmed live:
//
//	{"effect":{"action":"allow","obligations":{"mfa":{"required":true},
//	  "userVerification":{"requirement":"none"}}}}
//
// Notably simpler than jumpcloud_authentication_policy's effect object: no
// mfaFactors/mfaFactorSelectionMode were observed in the captured body, so
// this resource doesn't expose them -- the fallback policy's MFA requirement
// apparently isn't restricted to specific factor types the way a named policy's
// is.
func resourceDefaultAccessPolicy() *schema.Resource {
	return &schema.Resource{
		Description: "Manages one of JumpCloud's four fallback access policies " +
			"(\"Default Access Policy Settings\" on the MFA Configurations admin " +
			"page): application, userportal, adminportal, or ldap. Built from a " +
			"captured browser request -- not documented anywhere else.",
		Create: resourceDefaultAccessPolicyCreateUpdate,
		Read:   resourceDefaultAccessPolicyRead,
		Update: resourceDefaultAccessPolicyCreateUpdate,
		Delete: resourceDefaultAccessPolicyDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"resource_type": {
				Description: "Which fallback policy to manage: \"application\", " +
					"\"userportal\", \"adminportal\", or \"ldap\" (all four " +
					"confirmed live).",
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"effect_action": {
				Description: "The fallback policy's base access decision. " +
					"Confirmed live value: \"allow\".",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "allow",
			},
			"mfa_required": {
				Description: "Require MFA under this fallback policy.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
			},
			"user_verification": {
				Description: "WebAuthn-style user verification requirement: " +
					"\"discouraged\", \"preferred\", or \"required\" (values " +
					"confirmed on jumpcloud_authentication_policy and " +
					"jumpcloud_push_settings; not independently varied for this " +
					"resource during capture -- only \"none\" was observed).",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "none",
			},
		},
	}
}

func defaultAccessPolicyWrite(config *jcapiv2.Configuration, resourceType string, d *schema.ResourceData) error {
	apiKey := config.DefaultHeader["x-api-key"]
	body := map[string]interface{}{
		"effect": map[string]interface{}{
			"action": d.Get("effect_action").(string),
			"obligations": map[string]interface{}{
				"mfa": map[string]interface{}{
					"required": d.Get("mfa_required").(bool),
				},
				"userVerification": map[string]interface{}{
					"requirement": d.Get("user_verification").(string),
				},
			},
		},
	}
	_, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/authn/policy/fallback/"+resourceType, body)
	return err
}

func resourceDefaultAccessPolicyCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	resourceType := d.Get("resource_type").(string)

	if err := defaultAccessPolicyWrite(config, resourceType, d); err != nil {
		return err
	}
	d.SetId(resourceType)
	return resourceDefaultAccessPolicyRead(d, meta)
}

func resourceDefaultAccessPolicyRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	raw, err := jcGetRaw(config.BasePath, apiKey, "/authn/policy/fallback/"+d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	if err := d.Set("resource_type", d.Id()); err != nil {
		return err
	}
	if effect, ok := raw["effect"].(map[string]interface{}); ok {
		if v, ok := effect["action"].(string); ok {
			if err := d.Set("effect_action", v); err != nil {
				return err
			}
		}
		if obligations, ok := effect["obligations"].(map[string]interface{}); ok {
			if mfa, ok := obligations["mfa"].(map[string]interface{}); ok {
				if v, ok := mfa["required"].(bool); ok {
					if err := d.Set("mfa_required", v); err != nil {
						return err
					}
				}
			}
			if uv, ok := obligations["userVerification"].(map[string]interface{}); ok {
				if v, ok := uv["requirement"].(string); ok {
					if err := d.Set("user_verification", v); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func resourceDefaultAccessPolicyDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	resourceType := d.Id()

	// Reset to the safe default (no MFA required) rather than leaving
	// whatever was last configured -- this is a true singleton (JumpCloud
	// always has one fallback policy per resource type; there's nothing to
	// actually delete).
	body := map[string]interface{}{
		"effect": map[string]interface{}{
			"action": "allow",
			"obligations": map[string]interface{}{
				"mfa":              map[string]interface{}{"required": false},
				"userVerification": map[string]interface{}{"requirement": "none"},
			},
		},
	}
	apiKey := config.DefaultHeader["x-api-key"]
	if _, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/authn/policy/fallback/"+resourceType, body); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
