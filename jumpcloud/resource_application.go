package jumpcloud

import (
	"errors"
	"fmt"
	"log"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"golang.org/x/net/context"
)

type Constant struct {
	name      string
	value     string
	read_only bool
	required  bool
	visible   bool
}

func resourceApplication() *schema.Resource {
	return &schema.Resource{
		Description: "Provides a resource for adding an Amazon Web Services (AWS) account application. **Note:** This resource is due to change in future versions to be more generic and allow for adding various applications supported by JumpCloud.",
		Create:      resourceApplicationCreate,
		Read:        resourceApplicationRead,
		Update:      resourceApplicationUpdate,
		Delete:      resourceApplicationDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"name": {
				Description: "Name of the application",
				Type:        schema.TypeString,
				Required:    true,
			},
			"beta": {
				Description: "",
				Type:        schema.TypeBool,
				Default:     false,
				Optional:    true,
			},
			"active": {
				Description: "Whether single sign-on is active for this application. JumpCloud " +
					"shows an inactive application as \"Single Sign-On Inactive\" in the console " +
					"and creates new applications inactive. Omit it to leave the current state " +
					"unchanged (for example SSO activated in the console); set true or false to " +
					"manage it.",
				Type:     schema.TypeBool,
				Optional: true,
				Computed: true,
			},
			"display_label": {
				Description: "Name of the application to display",
				Type:        schema.TypeString,
				Required:    true,
			},
			"sso_url": {
				Description: "The SSO URL suffix to use",
				Type:        schema.TypeString,
				Required:    true,
			},
			"learn_more": {
				Description: "The application's \"learn more\" link. Omit it to leave the " +
					"value set in JumpCloud unchanged. Setting it to an empty string has no " +
					"effect; clear it in the JumpCloud console.",
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"constant_attributes": {
				Description: "",
				Type:        schema.TypeList,
				Optional:    true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:     schema.TypeString,
							Required: true,
						},
						"value": {
							Type:     schema.TypeString,
							Required: true,
						},
						"read_only": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
						},
						"required": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
						},
						"visible": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
						},
					},
				},
			},
			"database_attributes": {
				Description: "Maps JumpCloud user profile fields to attribute names sent to the " +
					"service provider. `name` is the attribute name the service provider expects " +
					"(e.g. \"email\", \"firstName\", \"lastName\"); `value` is the corresponding " +
					"JumpCloud user profile property name (e.g. \"email\", \"firstname\", " +
					"\"lastname\") -- confirmed against a captured browser request. Omit the " +
					"block entirely to leave the mappings configured in JumpCloud unchanged; " +
					"once set, the configured rows replace the existing ones. Mappings cannot be " +
					"cleared through Terraform once any exist: removing every block (or using a " +
					"dynamic block with an empty for_each) leaves them in place, and `= []` or " +
					"`= null` is not valid for a block. To remove all of them, delete them in the " +
					"JumpCloud console.",
				Type:     schema.TypeList,
				Optional: true,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Description: "The attribute name sent to the service provider.",
							Type:        schema.TypeString,
							Required:    true,
						},
						"value": {
							Description: "The JumpCloud user profile property name.",
							Type:        schema.TypeString,
							Required:    true,
						},
					},
				},
			},
			"include_groups": {
				Description: "Include the user's JumpCloud group memberships as a SAML attribute, " +
					"named by groups_attribute_name. Omit it to leave whatever is configured in JumpCloud unchanged.",
				Type:     schema.TypeBool,
				Optional: true,
				Computed: true,
			},
			"groups_attribute_name": {
				Description: "The SAML attribute name used for group memberships. Only meaningful " +
					"when include_groups is true. Omit it to leave whatever is configured in JumpCloud unchanged.",
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"sign_assertion": {
				Description: "Sign the SAML assertion (JumpCloud's default for new SAML " +
					"applications is true). Omit it to leave whatever is configured in JumpCloud unchanged.",
				Type:     schema.TypeBool,
				Optional: true,
				Computed: true,
			},
			"sign_response": {
				Description: "Sign the SAML response. Omit it to leave whatever is configured in JumpCloud unchanged.",
				Type:        schema.TypeBool,
				Optional:    true,
				Computed:    true,
			},
			"declare_redirect_endpoint": {
				Description: "Declare an HTTP-Redirect binding endpoint in the IdP metadata. Some " +
					"service providers (e.g. Claude) require this to be set. Omit it to leave whatever is configured in JumpCloud unchanged.",
				Type:     schema.TypeBool,
				Optional: true,
				Computed: true,
			},
			"idp_certificate": {
				Description: "Leave unset to have JumpCloud generate and manage its own signing " +
					"certificate -- confirmed against the live API as the correct way to use this " +
					"resource; a self-supplied certificate is stored as an opaque string and causes " +
					"the metadata-XML endpoint to fail with a 500 for every request. Set only if you " +
					"specifically need to supply your own IdP keypair instead.",
				Type:      schema.TypeString,
				Optional:  true,
				Computed:  true,
				Sensitive: true,
			},
			"idp_entity_id": {
				Description: "",
				Type:        schema.TypeString,
				Required:    true,
			},
			"idp_private_key": {
				Description: "Leave unset -- see idp_certificate. Never refreshed by Read even if " +
					"set: the API always returns this field empty regardless of what's stored.",
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"sp_entity_id": {
				Description: "",
				Type:        schema.TypeString,
				Required:    true,
			},
			"acs_url": {
				Description: "",
				Type:        schema.TypeString,
				Required:    true,
			},
			"metadata_xml": {
				Description: "The JumpCloud metadata XML file.",
				Type:        schema.TypeString,
				Computed:    true,
			},
		},
	}
}

func resourceApplicationCreate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))

	// The body is deliberately not logged: it carries idp_private_key (and
	// idp_certificate), which would otherwise end up in TF_LOG output.
	body := buildApplicationRequestBody(nil, d)

	result, err := ApplicationCreateRaw(configv1.BasePath, configv1.DefaultHeader, body)
	if err != nil {
		return err
	}

	id, _ := result["_id"].(string)
	if id == "" {
		return fmt.Errorf("application created but no _id was returned")
	}
	log.Println("[INFO] id=", id)
	d.SetId(id)
	return resourceApplicationRead(d, meta)
}

func resourceApplicationRead(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	apiKey := configv1.DefaultHeader["x-api-key"]

	raw, err := ApplicationGetRaw(configv1.BasePath, configv1.DefaultHeader, d.Id())
	if err != nil {
		if errors.Is(err, ErrApplicationNotFound) {
			log.Printf("[WARN] application %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}

	// A successful GET of /applications/{id} is that application; don't let a
	// response that omits _id drop it from state.
	id := d.Id()
	if v, _ := raw["_id"].(string); v != "" {
		id = v
		d.SetId(id)
	}

	if v, ok := raw["name"].(string); ok {
		if err := d.Set("name", v); err != nil {
			return err
		}
	}
	if v, ok := raw["beta"].(bool); ok {
		if err := d.Set("beta", v); err != nil {
			return err
		}
	}
	if v, ok := raw["active"].(bool); ok {
		if err := d.Set("active", v); err != nil {
			return err
		}
	}
	if v, ok := raw["displayLabel"].(string); ok {
		if err := d.Set("display_label", v); err != nil {
			return err
		}
	}
	// Always set (absent -> ""), so a configured value JumpCloud doesn't have
	// shows up as a change and gets sent.
	if err := d.Set("learn_more", stringOr(raw["learnMore"], "")); err != nil {
		return err
	}
	if v, ok := raw["ssoUrl"].(string); ok {
		if err := d.Set("sso_url", v); err != nil {
			return err
		}
	}

	// idp_private_key is intentionally not refreshed here: it is a write-only secret,
	// and overwriting the configured value with whatever the API returns for it
	// (masked or empty) would produce a permanent diff on every plan.
	if cfg, ok := raw["config"].(map[string]interface{}); ok {
		if v, ok := configFieldString(cfg, "spEntityId"); ok {
			if err := d.Set("sp_entity_id", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldString(cfg, "acsUrl"); ok {
			if err := d.Set("acs_url", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldString(cfg, "idpEntityId"); ok {
			if err := d.Set("idp_entity_id", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldString(cfg, "idpCertificate"); ok {
			if err := d.Set("idp_certificate", v); err != nil {
				return err
			}
		}
		if err := d.Set("constant_attributes", attributeRows(cfg, "constantAttributes")); err != nil {
			return err
		}
		if err := d.Set("database_attributes", databaseAttributeRows(cfg)); err != nil {
			return err
		}
		if v, ok := configFieldBool(cfg, "includeGroups"); ok {
			if err := d.Set("include_groups", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldString(cfg, "groupsAttributeName"); ok {
			if err := d.Set("groups_attribute_name", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldBool(cfg, "signAssertion"); ok {
			if err := d.Set("sign_assertion", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldBool(cfg, "signResponse"); ok {
			if err := d.Set("sign_response", v); err != nil {
				return err
			}
		}
		if v, ok := configFieldBool(cfg, "declareRedirectEndpoint"); ok {
			if err := d.Set("declare_redirect_endpoint", v); err != nil {
				return err
			}
		}
	}

	if id != "" {
		log.Println("[INFO] response ID is ", id)
		orgId := configv1.DefaultHeader["x-org-id"]

		metadataXml, err := GetApplicationMetadataXml(configv1.BasePath, orgId, id, apiKey)
		if err != nil {
			// org_id is optional, but the metadata URL is scoped to an organization. Without it
			// the fetch can fail on every refresh, so warn instead of failing Read; otherwise
			// single-org setups could never plan, and a freshly created app would be tainted.
			if orgId != "" {
				return err
			}
			log.Printf("[WARN] skipping metadata_xml for application %s: no org_id configured and the fetch failed: %s", id, err)
			metadataXml = ""
		}

		if err := d.Set("metadata_xml", metadataXml); err != nil {
			return err
		}
	} else {
		log.Println("[INFO] no ID in response, skipping metadata XML retrieval")
	}

	return nil
}

func resourceApplicationUpdate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))

	// Read-modify-write: start from the application's current full server-side state
	// (not just the fields this provider manages) and mutate only the managed fields
	// in place. JumpCloud's config object has many fields this resource doesn't
	// expose (signatureAlgorithm, subjectField, overrideNameIdFormat, spCertificate,
	// authClaimConfiguration, and more -- confirmed via a captured browser request).
	// The console itself always does a full read-modify-write on save; sending a
	// fresh, partial body on update risks silently resetting those unmanaged fields.
	current, err := ApplicationGetRaw(configv1.BasePath, configv1.DefaultHeader, d.Id())
	if err != nil {
		return err
	}

	body := buildApplicationRequestBody(current, d)
	if _, err := ApplicationUpdateRaw(configv1.BasePath, configv1.DefaultHeader, d.Id(), body); err != nil {
		return err
	}
	return resourceApplicationRead(d, meta)
}

func resourceApplicationDelete(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)

	_, httpRes, err := client.ApplicationsApi.ApplicationsDelete(context.TODO(), d.Id(), nil)
	// An application that is already gone (e.g. deleted in the console) is the desired end state
	if err != nil && !isNotFound(httpRes, err) {
		return err
	}

	d.SetId("")
	return nil
}

// buildApplicationRequestBody applies this resource's Terraform-managed fields onto
// seed (nil for Create, or the application's current raw state for Update) and
// returns the result as the request body. For Update, every key in seed that this
// resource doesn't manage is left untouched.
func buildApplicationRequestBody(seed map[string]interface{}, d *schema.ResourceData) map[string]interface{} {
	body := seed
	if body == nil {
		body = map[string]interface{}{}
	}

	body["name"] = d.Get("name").(string)
	body["displayLabel"] = d.Get("display_label").(string)
	body["ssoUrl"] = d.Get("sso_url").(string)
	body["beta"] = d.Get("beta").(bool)

	// The settings #30 added (active and the SAML options below) were never
	// managed before, so existing applications may have them configured in the
	// console. They are Optional+Computed and only written when the user sets
	// them (Create) or changes them (Update); otherwise the current value --
	// from seed on Update, JumpCloud's own default on Create -- is left alone.
	isCreate := seed == nil
	manages := func(key string) bool {
		if isCreate {
			_, ok := d.GetOkExists(key)
			return ok
		}
		return d.HasChange(key)
	}
	if manages("active") {
		body["active"] = d.Get("active").(bool)
	}
	// learn_more was never sent before (the request builders had no field
	// for it), so a configured value silently did nothing.
	if manages("learn_more") {
		body["learnMore"] = d.Get("learn_more").(string)
	}

	cfg, ok := body["config"].(map[string]interface{})
	if !ok {
		cfg = map[string]interface{}{}
		body["config"] = cfg
	}

	setConfigField(cfg, "idpEntityId", "text", "IdP Entity ID:", d.Get("idp_entity_id").(string), true, 0)
	setConfigField(cfg, "spEntityId", "text", "SP Entity ID:", d.Get("sp_entity_id").(string), true, 3)
	setConfigField(cfg, "acsUrl", "text", "ACS URL:", d.Get("acs_url").(string), true, 4)

	// Omit idpCertificate/idpPrivateKey from the request entirely when unset, matching
	// the shape JumpCloud's own console sends (confirmed via a captured browser
	// request) so JumpCloud generates its own signing certificate. Sending an empty
	// "value" with required:true instead -- what the generated SDK's omitempty tags
	// used to produce -- was confirmed live to 503 the create request every time.
	if v := d.Get("idp_certificate").(string); v != "" {
		setConfigField(cfg, "idpCertificate", "file", "IdP Certificate:", v, false, 2)
	}
	if v := d.Get("idp_private_key").(string); v != "" {
		setConfigField(cfg, "idpPrivateKey", "file", "IdP Private Key:", v, true, 1)
	}

	cfg["constantAttributes"] = attributeRowsField(d, "constant_attributes", "Constant Attributes:", 8)
	if manages("database_attributes") {
		cfg["databaseAttributes"] = databaseAttributesField(d)
	}

	if manages("include_groups") {
		setConfigField(cfg, "includeGroups", "bool", "Include Group Attribute", d.Get("include_groups").(bool), false, 10)
	}
	if manages("groups_attribute_name") {
		setConfigField(cfg, "groupsAttributeName", "text", "Groups Attribute Name", d.Get("groups_attribute_name").(string), false, 11)
	}
	if manages("sign_assertion") {
		setConfigField(cfg, "signAssertion", "bool", "Sign Assertion", d.Get("sign_assertion").(bool), false, 13)
	}
	if manages("sign_response") {
		setConfigField(cfg, "signResponse", "bool", "Sign Response", d.Get("sign_response").(bool), false, 13)
	}
	if manages("declare_redirect_endpoint") {
		setConfigField(cfg, "declareRedirectEndpoint", "bool", "Declare Redirect Endpoint", d.Get("declare_redirect_endpoint").(bool), false, 16)
	}

	return body
}

// setConfigField writes one of the config object's uniform {type,label,value,...}
// sub-objects -- the shape every field in JumpCloud's real config payload uses
// (confirmed via a captured browser request).
func setConfigField(cfg map[string]interface{}, key, type_, label string, value interface{}, required bool, position int) {
	cfg[key] = map[string]interface{}{
		"type":     type_,
		"label":    label,
		"value":    value,
		"required": required,
		"visible":  true,
		"readOnly": false,
		"position": position,
	}
}

func attributeRowsField(d *schema.ResourceData, schemaKey, label string, position int) map[string]interface{} {
	raw := d.Get(schemaKey).([]interface{})
	rows := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		item := r.(map[string]interface{})
		rows = append(rows, map[string]interface{}{
			"name":     item["name"].(string),
			"value":    item["value"].(string),
			"readOnly": item["read_only"].(bool),
			"required": item["required"].(bool),
			"visible":  item["visible"].(bool),
		})
	}
	return map[string]interface{}{
		"type":     "constantAttributes",
		"label":    label,
		"value":    rows,
		"required": false,
		"visible":  true,
		"readOnly": false,
		"position": position,
	}
}

// databaseAttributesField builds the User Attributes (service-provider <-> JumpCloud
// user property) mapping. Row shape confirmed via a captured browser request:
// isCustomAttribute/mutable/readOnly/tempId are UI bookkeeping fields the console
// always sends; tempId looks purely like a client-generated React key, but a
// deterministic per-row value is used here rather than omitting it, to stay as
// close as possible to the proven-working shape.
func databaseAttributesField(d *schema.ResourceData) map[string]interface{} {
	raw := d.Get("database_attributes").([]interface{})
	rows := make([]map[string]interface{}, 0, len(raw))
	for i, r := range raw {
		item := r.(map[string]interface{})
		rows = append(rows, map[string]interface{}{
			"isCustomAttribute": true,
			"mutable":           true,
			"readOnly":          false,
			"tempId":            fmt.Sprintf("tf-%d", i),
			"name":              item["name"].(string),
			"value":             item["value"].(string),
		})
	}
	return map[string]interface{}{
		"type":     "constantAttributes",
		"label":    "User Attributes:",
		"value":    rows,
		"required": false,
		"visible":  true,
		"readOnly": false,
		"position": 9,
	}
}

func configFieldString(cfg map[string]interface{}, key string) (string, bool) {
	field, ok := cfg[key].(map[string]interface{})
	if !ok {
		return "", false
	}
	v, ok := field["value"].(string)
	return v, ok
}

func configFieldBool(cfg map[string]interface{}, key string) (bool, bool) {
	field, ok := cfg[key].(map[string]interface{})
	if !ok {
		return false, false
	}
	v, ok := field["value"].(bool)
	return v, ok
}

// attributeRows extracts a constant_attributes-shaped list (name/value/read_only/
// required/visible) from a config field, used for constant_attributes itself.
func attributeRows(cfg map[string]interface{}, key string) []map[string]interface{} {
	field, ok := cfg[key].(map[string]interface{})
	if !ok {
		return nil
	}
	rawRows, ok := field["value"].([]interface{})
	if !ok {
		return nil
	}
	rows := make([]map[string]interface{}, 0, len(rawRows))
	for _, r := range rawRows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := row["name"].(string)
		value, _ := row["value"].(string)
		readOnly, _ := row["readOnly"].(bool)
		required, _ := row["required"].(bool)
		visible, _ := row["visible"].(bool)
		rows = append(rows, map[string]interface{}{
			"name":      name,
			"value":     value,
			"read_only": readOnly,
			"required":  required,
			"visible":   visible,
		})
	}
	return rows
}

// databaseAttributeRows extracts the database_attributes-shaped list (name/value
// only -- the other row fields are UI bookkeeping this resource doesn't manage)
// from the databaseAttributes config field.
func databaseAttributeRows(cfg map[string]interface{}) []map[string]interface{} {
	field, ok := cfg["databaseAttributes"].(map[string]interface{})
	if !ok {
		return nil
	}
	rawRows, ok := field["value"].([]interface{})
	if !ok {
		return nil
	}
	rows := make([]map[string]interface{}, 0, len(rawRows))
	for _, r := range rawRows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := row["name"].(string)
		value, _ := row["value"].(string)
		rows = append(rows, map[string]interface{}{
			"name":  name,
			"value": value,
		})
	}
	return rows
}
