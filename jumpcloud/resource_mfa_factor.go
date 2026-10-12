package jumpcloud

import (
	"errors"
	"log"
	"net/http"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// resourceMfaFactor manages whether a given MFA factor type is enabled
// org-wide, via PUT /userportal/mfa/{factorType} (v1). Confirmed via a
// captured browser request from JumpCloud's own "MFA Configurations" admin
// page ("userportal" in the path is JumpCloud's internal naming for this
// admin-facing API area, not an indication of per-user scope -- confirmed
// this setting is org-wide, not personal, directly with the user who
// captured it).
//
// Neither jcapi-go (this provider's Go SDK) nor jc-cli (JumpCloud's own
// actively maintained CLI) document this endpoint. jc-cli's own code claims
// "JumpCloud exposes no public MFA configuration endpoint" at all -- that
// claim describes the limits of jc-cli's own OpenAPI spec coverage, not the
// live API: jc-cli's own coverage tracking explicitly excludes
// "console-internal" endpoints from its command surface, and this is
// evidently one of them.
func resourceMfaFactor() *schema.Resource {
	return &schema.Resource{
		Description: "Manages whether an MFA factor type (e.g. \"webauthn\", " +
			"\"totp\", \"push\", \"sms\") is enabled org-wide. Built from a " +
			"captured browser request against JumpCloud's \"MFA Configurations\" " +
			"admin page -- not documented anywhere else, including this " +
			"provider's Go SDK and JumpCloud's own actively maintained CLI.",
		Create: resourceMfaFactorCreateUpdate,
		Read:   resourceMfaFactorRead,
		Update: resourceMfaFactorCreateUpdate,
		Delete: resourceMfaFactorDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"factor_type": {
				Description: "The MFA factor type. Confirmed values from live " +
					"captures: \"webauthn\" and \"totp\". The full set of valid " +
					"factor type strings (e.g. \"push\", \"sms\") is not " +
					"confirmed -- verify each one live before relying on it.",
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"enabled": {
				Description: "Whether this factor type is enabled org-wide.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
			},
			"read_only": {
				Description: "Whether this factor's enabled state is " +
					"server-controlled and can't be changed (e.g. a factor " +
					"JumpCloud always keeps available). Confirmed present on a " +
					"live GET response; semantics beyond that aren't confirmed.",
				Type:     schema.TypeBool,
				Computed: true,
			},
		},
	}
}

func mfaFactorWrite(configv1 *jcapiv1.Configuration, factorType string, enabled bool) error {
	apiKey := configv1.DefaultHeader["x-api-key"]
	_, err := jcWriteRaw(http.MethodPut, configv1.BasePath, apiKey, "/userportal/mfa/"+factorType, map[string]interface{}{
		"type":    factorType,
		"enabled": enabled,
	})
	return err
}

func resourceMfaFactorCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	factorType := d.Get("factor_type").(string)
	enabled := d.Get("enabled").(bool)

	if err := mfaFactorWrite(configv1, factorType, enabled); err != nil {
		return err
	}

	d.SetId(factorType)
	return resourceMfaFactorRead(d, meta)
}

func resourceMfaFactorRead(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	apiKey := configv1.DefaultHeader["x-api-key"]

	raw, err := jcGetRaw(configv1.BasePath, apiKey, "/userportal/mfa/"+d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		// GET is confirmed to work for webauthn and totp (a live capture of
		// the latter returned {"type":"totp","enabled":true,"id":"",
		// "readOnly":false}), but not every factor_type is verified yet.
		// Don't fail plan/apply over an unverified one -- leave the
		// already-recorded state as the source of truth.
		log.Printf("[WARN] skipping refresh for mfa factor %s: %s", d.Id(), err)
		return nil
	}

	if err := d.Set("factor_type", d.Id()); err != nil {
		return err
	}
	if v, ok := raw["enabled"].(bool); ok {
		if err := d.Set("enabled", v); err != nil {
			return err
		}
	}
	if v, ok := raw["readOnly"].(bool); ok {
		if err := d.Set("read_only", v); err != nil {
			return err
		}
	}
	return nil
}

func resourceMfaFactorDelete(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	factorType := d.Get("factor_type").(string)

	if err := mfaFactorWrite(configv1, factorType, false); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
