package jumpcloud

import (
	"errors"
	"fmt"
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// resourcePushSettings manages JumpCloud's org-wide Push (JumpCloud Protect)
// factor configuration, via the v2 /push/configs endpoint. Like
// jumpcloud_webauthn_settings, it's a singleton over an existing, auto-created
// config object discovered via GET /push/configs (list) -- but unlike
// webauthn's config, a captured PUT here sent the full object back
// ({"id":...,"userVerification":...,"concurrentLimit":{...},
// "isTotpIntegrated":...,"exclusionGroups":[...],"readOnly":...}), not a
// minimal partial update. Update therefore does a read-modify-write: it
// GETs the current object to preserve readOnly (server-controlled, never
// user-set) and id, and overlays this resource's managed fields on top.
func resourcePushSettings() *schema.Resource {
	return &schema.Resource{
		Description: "Manages JumpCloud's org-wide Push (JumpCloud Protect) " +
			"factor configuration. Built from a captured browser request " +
			"against JumpCloud's \"MFA Configurations\" admin page (Push's " +
			"\"Additional Settings\" section) -- not documented anywhere else.",
		Create: resourcePushSettingsCreateUpdate,
		Read:   resourcePushSettingsRead,
		Update: resourcePushSettingsCreateUpdate,
		Delete: resourcePushSettingsDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"config_id": {
				Description: "The Push config object's id. Auto-discovered via " +
					"GET /push/configs when unset.",
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"user_verification": {
				Description: "WebAuthn-style user verification requirement for " +
					"push approval: \"discouraged\", \"preferred\", or " +
					"\"required\" (all three confirmed live).",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "discouraged",
			},
			"concurrent_limit_enabled": {
				Description: "Whether a concurrent push-approval limit is enforced.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
			},
			"concurrent_limit_max": {
				Description: "Maximum concurrent push approvals, when " +
					"concurrent_limit_enabled is true.",
				Type:     schema.TypeInt,
				Optional: true,
				Default:  1,
			},
			"is_totp_integrated": {
				Description: "Controls \"combined\" (true) vs \"separate\" " +
					"(false) TOTP behavior -- confirmed live: setting this to " +
					"false is what causes TOTP to appear as its own standalone " +
					"factor (managed via jumpcloud_mfa_factor with " +
					"factor_type = \"totp\") rather than being combined into " +
					"this push factor.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"exclusion_groups": {
				Description: "User group IDs excluded from this push " +
					"configuration.",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

func resolvePushConfigId(config *jcapiv2.Configuration, d *schema.ResourceData) (string, error) {
	if v, ok := d.GetOk("config_id"); ok {
		return v.(string), nil
	}

	headers := config.DefaultHeader
	configs, err := jcListArrayRaw(config.BasePath, headers, "/push/configs")
	if err != nil {
		return "", err
	}
	if len(configs) == 0 {
		return "", fmt.Errorf("no push configs visible to this API key; set config_id explicitly")
	}
	id, _ := configs[0]["id"].(string)
	if id == "" {
		id, _ = configs[0]["_id"].(string)
	}
	if id == "" {
		return "", fmt.Errorf("could not determine push config id from /push/configs response")
	}
	return id, nil
}

func resourcePushSettingsCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	configId, err := resolvePushConfigId(config, d)
	if err != nil {
		return err
	}

	// The PUT needs the full object back, including server-controlled fields
	// like readOnly, so overlay the managed fields onto the current object. If
	// the GET fails, abort instead of writing guessed values for those fields.
	body, err := jcGetRaw(config.BasePath, headers, "/push/configs/"+configId)
	if err != nil {
		return err
	}
	body["id"] = configId
	body["userVerification"] = d.Get("user_verification").(string)
	body["isTotpIntegrated"] = d.Get("is_totp_integrated").(bool)
	body["exclusionGroups"] = stringSliceFromInterfaceList(d.Get("exclusion_groups").([]interface{}))
	limit, ok := body["concurrentLimit"].(map[string]interface{})
	if !ok {
		limit = map[string]interface{}{}
		body["concurrentLimit"] = limit
	}
	limit["enabled"] = d.Get("concurrent_limit_enabled").(bool)
	limit["max"] = d.Get("concurrent_limit_max").(int)

	if _, err := jcWriteRaw(http.MethodPut, config.BasePath, headers, "/push/configs/"+configId, body); err != nil {
		return err
	}

	d.SetId(configId)
	return resourcePushSettingsRead(d, meta)
}

func resourcePushSettingsRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	raw, err := jcGetRaw(config.BasePath, headers, "/push/configs/"+d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	if err := d.Set("config_id", d.Id()); err != nil {
		return err
	}
	// Set unconditionally, defaulting when the response omits a field, so a
	// missing field can't leave a stale value in state.
	limit, _ := raw["concurrentLimit"].(map[string]interface{})
	for k, v := range map[string]interface{}{
		"user_verification":        stringOr(raw["userVerification"], "discouraged"),
		"is_totp_integrated":       boolOr(raw["isTotpIntegrated"], true),
		"exclusion_groups":         stringSliceFromAny(raw["exclusionGroups"]),
		"concurrent_limit_enabled": boolOr(limit["enabled"], true),
		"concurrent_limit_max":     intOr(limit["max"], 1),
	} {
		if err := d.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}

func resourcePushSettingsDelete(d *schema.ResourceData, meta interface{}) error {
	// There's nothing meaningful to reset this config object to on destroy --
	// it's a singleton JumpCloud itself created, not something Terraform
	// creates or deletes. Just stop tracking it.
	d.SetId("")
	return nil
}
