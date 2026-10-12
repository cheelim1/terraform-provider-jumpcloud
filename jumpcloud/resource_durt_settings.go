package jumpcloud

import (
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// durtSettingsID is a fixed, synthetic Terraform resource id: the underlying
// JumpCloud object is a true singleton at a fixed path (/durt/settings), with
// no id of its own to key off -- same pattern as jumpcloud_mfa_enrollment_policy.
const durtSettingsID = "durtSettings"

// resourceDurtSettings manages JumpCloud's org-wide "JumpCloud Go" (DURT)
// factor configuration, via the v2 /durt/settings endpoint. Confirmed via a
// captured PUT: {"enabled":true,"readOnly":false}. readOnly is
// server-controlled, never user-set, so Update preserves whatever the API
// last reported for it rather than guessing a value.
func resourceDurtSettings() *schema.Resource {
	return &schema.Resource{
		Description: "Manages JumpCloud's org-wide \"JumpCloud Go\" (DURT) " +
			"factor configuration. Built from a captured browser request " +
			"against JumpCloud's \"MFA Configurations\" admin page -- not " +
			"documented anywhere else.",
		Create: resourceDurtSettingsCreateUpdate,
		Read:   resourceDurtSettingsRead,
		Update: resourceDurtSettingsCreateUpdate,
		Delete: resourceDurtSettingsDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"enabled": {
				Description: "Whether JumpCloud Go (DURT) is enabled org-wide. " +
					"Destroying this resource resets it to false.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func durtSettingsWrite(config *jcapiv2.Configuration, enabled bool) error {
	apiKey := config.DefaultHeader["x-api-key"]

	// readOnly is server-controlled, never user-set -- preserve whatever the
	// API last reported rather than guessing a value for it.
	readOnly := false
	if current, err := jcGetRaw(config.BasePath, apiKey, "/durt/settings"); err == nil {
		if v, ok := current["readOnly"].(bool); ok {
			readOnly = v
		}
	}

	body := map[string]interface{}{
		"enabled":  enabled,
		"readOnly": readOnly,
	}
	_, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/durt/settings", body)
	return err
}

func resourceDurtSettingsCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	if err := durtSettingsWrite(config, d.Get("enabled").(bool)); err != nil {
		return err
	}
	d.SetId(durtSettingsID)
	return resourceDurtSettingsRead(d, meta)
}

func resourceDurtSettingsRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	raw, err := jcGetRaw(config.BasePath, apiKey, "/durt/settings")
	if err != nil {
		return err
	}

	d.SetId(durtSettingsID)
	if v, ok := raw["enabled"].(bool); ok {
		if err := d.Set("enabled", v); err != nil {
			return err
		}
	}
	return nil
}

func resourceDurtSettingsDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	if err := durtSettingsWrite(config, false); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
