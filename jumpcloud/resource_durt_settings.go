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
					"Destroying this resource resets it to false (unless JumpCloud " +
					"reports the setting as read-only, in which case it is left as is).",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

// durtSettingsWrite PUTs enabled while preserving the server-controlled
// readOnly flag. With skipIfReadOnly (destroy), a readOnly config is left
// untouched instead of written, since that write can only be rejected and
// would otherwise block destroy.
func durtSettingsWrite(config *jcapiv2.Configuration, enabled, skipIfReadOnly bool) error {
	headers := config.DefaultHeader

	// readOnly is server-controlled, never user-set -- preserve whatever the
	// API reports rather than guessing a value for it. If the GET fails, abort
	// instead of writing a guessed readOnly back.
	current, err := jcGetRaw(config.BasePath, headers, "/durt/settings")
	if err != nil {
		return err
	}
	readOnly, _ := current["readOnly"].(bool)
	if readOnly && skipIfReadOnly {
		return nil
	}

	body := map[string]interface{}{
		"enabled":  enabled,
		"readOnly": readOnly,
	}
	_, err = jcWriteRaw(http.MethodPut, config.BasePath, headers, "/durt/settings", body)
	return err
}

func resourceDurtSettingsCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	if err := durtSettingsWrite(config, d.Get("enabled").(bool), false); err != nil {
		return err
	}
	d.SetId(durtSettingsID)
	return resourceDurtSettingsRead(d, meta)
}

func resourceDurtSettingsRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	raw, err := jcGetRaw(config.BasePath, headers, "/durt/settings")
	if err != nil {
		return err
	}

	d.SetId(durtSettingsID)
	// Set unconditionally, defaulting when the response omits the field, so a
	// missing field can't leave a stale value in state.
	return d.Set("enabled", boolOr(raw["enabled"], false))
}

func resourceDurtSettingsDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	if err := durtSettingsWrite(config, false, true); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
