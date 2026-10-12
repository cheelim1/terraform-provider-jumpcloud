package jumpcloud

import (
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// mfaEnrollmentPolicyID is a fixed, synthetic Terraform resource id: the
// underlying JumpCloud object is a true singleton at a fixed path
// (/mfa/enrollmentPolicy), with no id of its own to key off.
const mfaEnrollmentPolicyID = "enrollmentPolicy"

// resourceMfaEnrollmentPolicy manages JumpCloud's org-wide MFA enrollment
// policy -- whether all users must enroll a primary (and optionally backup)
// MFA factor, and the grace period before enforcement begins. Built from a
// captured browser request/response against the v2 /mfa/enrollmentPolicy
// endpoint (GET to read, PUT to write) -- not documented in jcapi-go (this
// provider's Go SDK) or jc-cli (JumpCloud's own actively maintained CLI),
// whose own code claims no public MFA configuration endpoint exists at all.
// That claim describes the limits of jc-cli's own OpenAPI spec coverage, not
// the live API: jc-cli's own coverage tracking explicitly excludes
// "console-internal" endpoints from its command surface, and this is
// evidently one of them.
//
// The real object is a small, flat body with exactly the three fields this
// resource manages (confirmed via a captured PUT:
// {"requirePrimaryFactor":true,"gracePeriodDays":7,"requireBackupFactor":false}),
// so Update sends the whole object directly rather than a read-modify-write.
func resourceMfaEnrollmentPolicy() *schema.Resource {
	return &schema.Resource{
		Description: "Manages JumpCloud's org-wide MFA enrollment policy -- " +
			"whether all users must enroll an MFA factor, and the grace period " +
			"before enforcement begins. Built from a captured browser request " +
			"against JumpCloud's \"MFA Configurations\" admin page -- not " +
			"documented anywhere else, including this provider's Go SDK and " +
			"JumpCloud's own actively maintained CLI.",
		Create: resourceMfaEnrollmentPolicyCreateUpdate,
		Read:   resourceMfaEnrollmentPolicyRead,
		Update: resourceMfaEnrollmentPolicyCreateUpdate,
		Delete: resourceMfaEnrollmentPolicyDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"require_primary_factor": {
				Description: "Require every user to enroll a primary MFA " +
					"factor. Destroying this resource resets it to false.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"grace_period_days": {
				Description: "Days a user has to enroll a factor before " +
					"enforcement begins.",
				Type:     schema.TypeInt,
				Optional: true,
				Default:  0,
			},
			"require_backup_factor": {
				Description: "Require every user to also enroll a backup " +
					"MFA factor.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func mfaEnrollmentPolicyWrite(config *jcapiv2.Configuration, d *schema.ResourceData) error {
	apiKey := config.DefaultHeader["x-api-key"]
	body := map[string]interface{}{
		"requirePrimaryFactor": d.Get("require_primary_factor").(bool),
		"gracePeriodDays":      d.Get("grace_period_days").(int),
		"requireBackupFactor":  d.Get("require_backup_factor").(bool),
	}
	_, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/mfa/enrollmentPolicy", body)
	return err
}

func resourceMfaEnrollmentPolicyCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	if err := mfaEnrollmentPolicyWrite(config, d); err != nil {
		return err
	}
	d.SetId(mfaEnrollmentPolicyID)
	return resourceMfaEnrollmentPolicyRead(d, meta)
}

func resourceMfaEnrollmentPolicyRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	raw, err := jcGetRaw(config.BasePath, apiKey, "/mfa/enrollmentPolicy")
	if err != nil {
		return err
	}

	d.SetId(mfaEnrollmentPolicyID)
	if v, ok := raw["requirePrimaryFactor"].(bool); ok {
		if err := d.Set("require_primary_factor", v); err != nil {
			return err
		}
	}
	if v, ok := raw["gracePeriodDays"].(float64); ok {
		if err := d.Set("grace_period_days", int(v)); err != nil {
			return err
		}
	}
	if v, ok := raw["requireBackupFactor"].(bool); ok {
		if err := d.Set("require_backup_factor", v); err != nil {
			return err
		}
	}
	return nil
}

func resourceMfaEnrollmentPolicyDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	body := map[string]interface{}{
		"requirePrimaryFactor": false,
		"gracePeriodDays":      0,
		"requireBackupFactor":  false,
	}
	if _, err := jcWriteRaw(http.MethodPut, config.BasePath, apiKey, "/mfa/enrollmentPolicy", body); err != nil {
		return err
	}
	d.SetId("")
	return nil
}
