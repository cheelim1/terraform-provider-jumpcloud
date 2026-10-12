package jumpcloud

import (
	"encoding/json"
	"errors"
	"fmt"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// resourceAuthenticationPolicy manages a JumpCloud authentication policy
// (JumpCloud's conditional-access policy engine, at /authn/policies in the v2
// API). This is the current, documented mechanism for requiring MFA on user
// logins -- jcapi-go (this provider's Go SDK dependency, with no real code
// change since 2019) has no knowledge of this endpoint at all, and
// JumpCloud's own actively maintained CLI (TheJumpCloud/jc-cli) turned out
// not to be a reliable reference for this specific area either: its
// `auth-policies create` command builds a body shape that the live API
// rejects (confirmed live: a 400 "missing effect", then a second 400 with
// `effect` present as a string instead of an object), and its tests for that
// command run against a mocked local server, not the real API. Authentication
// Policies wasn't one of jc-cli's specially live-verified areas.
//
// The schema below is instead built from a captured browser request (the
// JumpCloud console's own Authentication Policies UI), which is the only
// confirmed-correct source for this endpoint's real shape.
func resourceAuthenticationPolicy() *schema.Resource {
	return &schema.Resource{
		Description: "Manages a JumpCloud authentication policy (conditional " +
			"access / MFA enforcement). Built from a captured browser request " +
			"against the JumpCloud console -- neither this provider's Go SDK nor " +
			"JumpCloud's own jc-cli reliably document this endpoint's shape.",
		Create: resourceAuthenticationPolicyCreate,
		Read:   resourceAuthenticationPolicyRead,
		Update: resourceAuthenticationPolicyUpdate,
		Delete: resourceAuthenticationPolicyDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"name": {
				Description: "Policy name.",
				Type:        schema.TypeString,
				Required:    true,
			},
			"description": {
				Description: "Policy description.",
				Type:        schema.TypeString,
				Optional:    true,
			},
			"type": {
				Description: "Policy type, e.g. \"user_portal\" or \"admin\".",
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
			},
			"disabled": {
				Description: "Whether the policy is disabled.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
			},
			"monitor_only": {
				Description: "Evaluate the policy and log the outcome without " +
					"actually enforcing it. Useful for testing a policy's reach " +
					"before it affects logins.",
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"conditions_json": {
				Description: "The policy's conditions tree, as a raw JSON string. " +
					"No typed schema is exposed for this -- confirmed via a " +
					"captured browser request to be an object ({} for no " +
					"conditions), but its shape beyond that isn't confirmed.",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "{}",
			},
			"effect_action": {
				Description: "The base access decision when this policy applies: " +
					"confirmed value from a live capture is \"allow\" (MFA and " +
					"other requirements are layered on via the mfa_* / " +
					"user_verification fields below, not via this value).",
				Type:     schema.TypeString,
				Required: true,
			},
			"mfa_required": {
				Description: "Require MFA under this policy.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
			},
			"mfa_factors": {
				Description: "Acceptable MFA factor types, e.g. [\"TOTP\", " +
					"\"PUSH\", \"DURT\"] (confirmed values from a live capture; " +
					"the full set of valid factor type strings is not confirmed).",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"mfa_factor_selection_mode": {
				Description: "How mfa_factors are combined, e.g. \"all\" " +
					"(confirmed value from a live capture; other possible values " +
					"are not confirmed).",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "all",
			},
			"user_verification_requirement": {
				Description: "WebAuthn user verification requirement, e.g. " +
					"\"none\" (confirmed value from a live capture; other " +
					"possible values are not confirmed).",
				Type:     schema.TypeString,
				Optional: true,
				Default:  "none",
			},
			"target_resource_types": {
				Description: "Resource types this policy targets, e.g. " +
					"[\"user_portal\"].",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"target_user_inclusions": {
				Description: "Which users this policy targets: [\"all\"] for " +
					"everyone, or specific user/group identifiers (shape beyond " +
					"\"all\" is not confirmed).",
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"target_excluded_applications": {
				Description: "Application IDs excluded from this policy's targets.",
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

func resourceAuthenticationPolicyCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	body, err := buildAuthPolicyRequestBody(nil, d)
	if err != nil {
		return err
	}

	result, err := AuthPolicyCreateRaw(config.BasePath, apiKey, body)
	if err != nil {
		return err
	}

	id, _ := result["id"].(string)
	if id == "" {
		id, _ = result["_id"].(string)
	}
	if id == "" {
		return fmt.Errorf("authentication policy created but no id was returned: %+v", result)
	}
	d.SetId(id)
	return resourceAuthenticationPolicyRead(d, meta)
}

func resourceAuthenticationPolicyRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	raw, err := AuthPolicyGetRaw(config.BasePath, apiKey, d.Id())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}

	if v, ok := raw["name"].(string); ok {
		if err := d.Set("name", v); err != nil {
			return err
		}
	}
	if v, ok := raw["description"].(string); ok {
		if err := d.Set("description", v); err != nil {
			return err
		}
	}
	if v, ok := raw["type"].(string); ok {
		if err := d.Set("type", v); err != nil {
			return err
		}
	}
	if v, ok := raw["disabled"].(bool); ok {
		if err := d.Set("disabled", v); err != nil {
			return err
		}
	}
	if v, ok := raw["monitorOnly"].(bool); ok {
		if err := d.Set("monitor_only", v); err != nil {
			return err
		}
	}
	if cond, ok := raw["conditions"]; ok && cond != nil {
		b, err := json.Marshal(cond)
		if err != nil {
			return fmt.Errorf("re-encoding conditions for state: %w", err)
		}
		if err := d.Set("conditions_json", string(b)); err != nil {
			return err
		}
	}

	if effect, ok := raw["effect"].(map[string]interface{}); ok {
		if v, ok := effect["action"].(string); ok {
			if err := d.Set("effect_action", v); err != nil {
				return err
			}
		}
		if obligations, ok := effect["obligations"].(map[string]interface{}); ok {
			if mfa, ok := obligations["mfa"].(map[string]interface{}); ok {
				required, _ := mfa["required"].(bool)
				if err := d.Set("mfa_required", required); err != nil {
					return err
				}
			}
			if uv, ok := obligations["userVerification"].(map[string]interface{}); ok {
				if v, ok := uv["requirement"].(string); ok {
					if err := d.Set("user_verification_requirement", v); err != nil {
						return err
					}
				}
			}
			if v, ok := obligations["mfaFactorSelectionMode"].(string); ok {
				if err := d.Set("mfa_factor_selection_mode", v); err != nil {
					return err
				}
			}
			if factors, ok := obligations["mfaFactors"].([]interface{}); ok {
				types := make([]string, 0, len(factors))
				for _, f := range factors {
					if fm, ok := f.(map[string]interface{}); ok {
						if t, ok := fm["type"].(string); ok {
							types = append(types, t)
						}
					}
				}
				if err := d.Set("mfa_factors", types); err != nil {
					return err
				}
			}
		}
	}

	if targets, ok := raw["targets"].(map[string]interface{}); ok {
		if resources, ok := targets["resources"].([]interface{}); ok {
			types := make([]string, 0, len(resources))
			for _, r := range resources {
				if rm, ok := r.(map[string]interface{}); ok {
					if t, ok := rm["type"].(string); ok {
						types = append(types, t)
					}
				}
			}
			if err := d.Set("target_resource_types", types); err != nil {
				return err
			}
		}
		if users, ok := targets["users"].(map[string]interface{}); ok {
			if err := d.Set("target_user_inclusions", stringSliceFromAny(users["inclusions"])); err != nil {
				return err
			}
		}
		if err := d.Set("target_excluded_applications", stringSliceFromAny(targets["excludedApplications"])); err != nil {
			return err
		}
	}

	return nil
}

func resourceAuthenticationPolicyUpdate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	current, err := AuthPolicyGetRaw(config.BasePath, apiKey, d.Id())
	if err != nil {
		return err
	}
	body, err := buildAuthPolicyRequestBody(current, d)
	if err != nil {
		return err
	}
	if _, err := AuthPolicyUpdateRaw(config.BasePath, apiKey, d.Id(), body); err != nil {
		return err
	}
	return resourceAuthenticationPolicyRead(d, meta)
}

func resourceAuthenticationPolicyDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	apiKey := config.DefaultHeader["x-api-key"]

	if err := AuthPolicyDeleteRaw(config.BasePath, apiKey, d.Id()); err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}
	d.SetId("")
	return nil
}

// buildAuthPolicyRequestBody applies this resource's managed fields onto seed
// (nil for Create, or the policy's current raw state for Update) and returns
// the result as the request body -- the same read-modify-write pattern used
// for jumpcloud_application's Update, since the real policy object may have
// fields this resource doesn't expose.
func buildAuthPolicyRequestBody(seed map[string]interface{}, d *schema.ResourceData) (map[string]interface{}, error) {
	body := seed
	if body == nil {
		body = map[string]interface{}{}
	}

	body["name"] = d.Get("name").(string)
	body["description"] = d.Get("description").(string)
	body["disabled"] = d.Get("disabled").(bool)
	body["monitorOnly"] = d.Get("monitor_only").(bool)
	if v, ok := d.GetOk("type"); ok {
		body["type"] = v.(string)
	}

	condJSON := d.Get("conditions_json").(string)
	if condJSON == "" {
		condJSON = "{}"
	}
	var cond interface{}
	if err := json.Unmarshal([]byte(condJSON), &cond); err != nil {
		return nil, fmt.Errorf("conditions_json is not valid JSON: %w", err)
	}
	body["conditions"] = cond

	mfaFactors := make([]map[string]interface{}, 0)
	for _, f := range d.Get("mfa_factors").([]interface{}) {
		mfaFactors = append(mfaFactors, map[string]interface{}{"type": f.(string)})
	}
	body["effect"] = map[string]interface{}{
		"action": d.Get("effect_action").(string),
		"obligations": map[string]interface{}{
			"mfa": map[string]interface{}{
				"required": d.Get("mfa_required").(bool),
			},
			"userVerification": map[string]interface{}{
				"requirement": d.Get("user_verification_requirement").(string),
			},
			"mfaFactors":             mfaFactors,
			"mfaFactorSelectionMode": d.Get("mfa_factor_selection_mode").(string),
		},
	}

	resourceTypes := d.Get("target_resource_types").([]interface{})
	resources := make([]map[string]interface{}, 0, len(resourceTypes))
	for _, t := range resourceTypes {
		resources = append(resources, map[string]interface{}{"type": t.(string)})
	}
	body["targets"] = map[string]interface{}{
		"resources": resources,
		"users": map[string]interface{}{
			"inclusions": stringSliceFromInterfaceList(d.Get("target_user_inclusions").([]interface{})),
		},
		"excludedApplications": stringSliceFromInterfaceList(d.Get("target_excluded_applications").([]interface{})),
		// userGroups isn't exposed as a managed field yet (nothing has asked
		// for group-scoped policies) -- confirmed present on the real object
		// via a captured PATCH, so it's included empty here rather than
		// omitted, matching the real schema as closely as possible.
		"userGroups": map[string]interface{}{
			"inclusions": []string{},
			"exclusions": []string{},
		},
	}

	return body, nil
}

// stringSliceFromInterfaceList converts a Terraform TypeList of strings
// (as returned by d.Get) into a []string.
func stringSliceFromInterfaceList(raw []interface{}) []string {
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

// stringSliceFromAny converts a raw JSON array value (as decoded into
// []interface{} by encoding/json) into a []string, for setting into a
// Terraform TypeList. A missing or non-array value yields an empty slice.
func stringSliceFromAny(raw interface{}) []string {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
