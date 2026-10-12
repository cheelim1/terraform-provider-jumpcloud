package jumpcloud

import (
	"errors"
	"fmt"
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// resourceWebauthnSettings manages JumpCloud's org-wide WebAuthn configuration
// (currently just allowSelfRegistration), via the v2 /webauthn/configs
// endpoint. It's a singleton over an existing, auto-created config object --
// Create/Delete manage only the fields this resource exposes, not the
// config object's lifecycle. The config's id isn't exposed anywhere a user
// would naturally have it, so it's auto-discovered via GET /webauthn/configs
// (confirmed via a captured browser request: the console itself does the
// same GET on page load, then PUTs to /webauthn/configs/{id}).
//
// Unlike jumpcloud_application and jumpcloud_authentication_policy, the
// captured PUT body here was a minimal partial object ({"allowSelfRegistration":
// true}), not the full config -- so this endpoint appears to support partial
// writes natively, and Update sends only the field this resource manages
// rather than a read-modify-write of the whole object.
func resourceWebauthnSettings() *schema.Resource {
	return &schema.Resource{
		Description: "Manages JumpCloud's org-wide WebAuthn configuration. " +
			"Built from a captured browser request against JumpCloud's \"MFA " +
			"Configurations\" admin page (WebAuthn's \"Additional Settings\" " +
			"section) -- not documented anywhere else.",
		Create: resourceWebauthnSettingsCreateUpdate,
		Read:   resourceWebauthnSettingsRead,
		Update: resourceWebauthnSettingsCreateUpdate,
		Delete: resourceWebauthnSettingsDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"config_id": {
				Description: "The WebAuthn config object's id. Auto-discovered " +
					"via GET /webauthn/configs when unset.",
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"allow_self_registration": {
				Description: "Allow security key self-registration for all " +
					"users. Defaults to false, matching JumpCloud's own default.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func resolveWebauthnConfigId(config *jcapiv2.Configuration, d *schema.ResourceData) (string, error) {
	if v, ok := d.GetOk("config_id"); ok {
		return v.(string), nil
	}

	apiKey := config.DefaultHeader["x-api-key"]
	configs, err := jcListArrayRaw(config.BasePath, apiKey, "/webauthn/configs")
	if err != nil {
		return "", err
	}
	if len(configs) == 0 {
		return "", fmt.Errorf("no webauthn configs visible to this API key; set config_id explicitly")
	}
	id, _ := configs[0]["_id"].(string)
	if id == "" {
		id, _ = configs[0]["id"].(string)
	}
	if id == "" {
		return "", fmt.Errorf("could not determine webauthn config id from /webauthn/configs response")
	}
	return id, nil
}

func resourceWebauthnSettingsCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	configId, err := resolveWebauthnConfigId(config, d)
	if err != nil {
		return err
	}

	body := map[string]interface{}{
		"allowSelfRegistration": d.Get("allow_self_registration").(bool),
	}
	if _, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/webauthn/configs/"+configId, body); err != nil {
		return err
	}

	d.SetId(configId)
	return resourceWebauthnSettingsRead(d, meta)
}

func resourceWebauthnSettingsRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	raw, err := jcGetRaw(config.BasePath, apiKey, "/webauthn/configs/"+d.Id())
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
	if v, ok := raw["allowSelfRegistration"].(bool); ok {
		if err := d.Set("allow_self_registration", v); err != nil {
			return err
		}
	}
	return nil
}

func resourceWebauthnSettingsDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	body := map[string]interface{}{"allowSelfRegistration": false}
	if _, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/webauthn/configs/"+d.Id(), body); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
