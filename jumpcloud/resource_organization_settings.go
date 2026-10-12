package jumpcloud

import (
	"errors"
	"fmt"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// resourceOrganizationSettings manages a subset of JumpCloud's organization-wide
// settings. It is a singleton over an existing organization, not a creatable/
// deletable JumpCloud object: Create finds (or is given) the organization and
// writes the managed settings onto it; Delete resets them to JumpCloud's own
// defaults rather than deleting the organization itself.
//
// Only requireAdminMFA is managed today. Per-factor MFA configuration
// (TOTP/WebAuthn/Push enablement, enrollment grace period) has no public API --
// confirmed against TheJumpCloud/jc-cli (JumpCloud's own actively maintained
// CLI, built against the current OpenAPI spec): its MFA dashboard reads these
// settings but its own code says plainly "JumpCloud exposes no public MFA
// configuration endpoint." requireAdminMFA, by contrast, is a documented,
// writable field on the organization object itself (PUT /organizations/{id}
// with {"settings": {...}}), confirmed via jc-cli's own `org update
// --settings-json` command.
func resourceOrganizationSettings() *schema.Resource {
	return &schema.Resource{
		Description: "Manages a subset of JumpCloud organization-wide settings. " +
			"This is a singleton over an existing organization -- Create/Delete " +
			"don't create or delete the organization itself, only the managed " +
			"settings on it.",
		Create: resourceOrganizationSettingsCreate,
		Read:   resourceOrganizationSettingsRead,
		Update: resourceOrganizationSettingsUpdate,
		Delete: resourceOrganizationSettingsDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"org_id": {
				Description: "The organization ID to manage. Leave unset for a " +
					"single-org account to auto-discover it (the first, and only, " +
					"result from GET /organizations).",
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				ForceNew: true,
			},
			"require_admin_mfa": {
				Description: "Require multi-factor authentication for JumpCloud " +
					"Admin Portal access. Destroying this resource resets it to " +
					"false, JumpCloud's own default.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
		},
	}
}

func resolveOrgId(configv1 *jcapiv1.Configuration, d *schema.ResourceData) (string, error) {
	if v, ok := d.GetOk("org_id"); ok {
		return v.(string), nil
	}

	apiKey := configv1.DefaultHeader["x-api-key"]
	orgs, err := OrganizationsListRaw(configv1.BasePath, apiKey)
	if err != nil {
		return "", err
	}
	if len(orgs) == 0 {
		return "", fmt.Errorf("no organizations visible to this API key; set org_id explicitly")
	}
	id, _ := orgs[0]["_id"].(string)
	if id == "" {
		id, _ = orgs[0]["id"].(string)
	}
	if id == "" {
		return "", fmt.Errorf("could not determine organization id from /organizations response")
	}
	return id, nil
}

func resourceOrganizationSettingsCreate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	apiKey := configv1.DefaultHeader["x-api-key"]

	orgId, err := resolveOrgId(configv1, d)
	if err != nil {
		return err
	}

	current, err := OrganizationGetRaw(configv1.BasePath, apiKey, orgId)
	if err != nil {
		return err
	}
	applyManagedOrgSettings(current, d)
	if _, err := OrganizationUpdateRaw(configv1.BasePath, apiKey, orgId, current); err != nil {
		return err
	}

	d.SetId(orgId)
	return resourceOrganizationSettingsRead(d, meta)
}

func resourceOrganizationSettingsRead(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	apiKey := configv1.DefaultHeader["x-api-key"]

	raw, err := OrganizationGetRaw(configv1.BasePath, apiKey, d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	if err := d.Set("org_id", d.Id()); err != nil {
		return err
	}

	settings, _ := raw["settings"].(map[string]interface{})
	requireAdminMFA, _ := settings["requireAdminMFA"].(bool)
	if err := d.Set("require_admin_mfa", requireAdminMFA); err != nil {
		return err
	}

	return nil
}

func resourceOrganizationSettingsUpdate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	apiKey := configv1.DefaultHeader["x-api-key"]

	current, err := OrganizationGetRaw(configv1.BasePath, apiKey, d.Id())
	if err != nil {
		return err
	}
	applyManagedOrgSettings(current, d)
	if _, err := OrganizationUpdateRaw(configv1.BasePath, apiKey, d.Id(), current); err != nil {
		return err
	}
	return resourceOrganizationSettingsRead(d, meta)
}

func resourceOrganizationSettingsDelete(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	apiKey := configv1.DefaultHeader["x-api-key"]

	current, err := OrganizationGetRaw(configv1.BasePath, apiKey, d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	settings, ok := current["settings"].(map[string]interface{})
	if !ok {
		settings = map[string]interface{}{}
		current["settings"] = settings
	}
	settings["requireAdminMFA"] = false

	if _, err := OrganizationUpdateRaw(configv1.BasePath, apiKey, d.Id(), current); err != nil {
		return err
	}

	d.SetId("")
	return nil
}

// applyManagedOrgSettings mutates raw's settings object in place, leaving any
// settings this resource doesn't manage (passwordPolicy, and anything else)
// untouched -- same read-modify-write safety as the application resource's
// Update, since organization settings is a rich object this resource only
// manages a slice of.
func applyManagedOrgSettings(raw map[string]interface{}, d *schema.ResourceData) {
	settings, ok := raw["settings"].(map[string]interface{})
	if !ok {
		settings = map[string]interface{}{}
		raw["settings"] = settings
	}
	settings["requireAdminMFA"] = d.Get("require_admin_mfa").(bool)
}
