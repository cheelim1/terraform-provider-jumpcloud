package jumpcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// userKeep is appended to the description of every user attribute that is
// Optional+Computed: omitting it leaves JumpCloud's value alone.
const userKeep = " Omit it to leave the value set in JumpCloud unchanged."

func resourceUser() *schema.Resource {
	optionalComputedBool := func(desc string) *schema.Schema {
		return &schema.Schema{Type: schema.TypeBool, Optional: true, Computed: true, Description: desc + userKeep}
	}
	optionalComputedString := func(desc string) *schema.Schema {
		return &schema.Schema{Type: schema.TypeString, Optional: true, Computed: true, Description: desc + userKeep}
	}
	return &schema.Resource{
		Create: resourceUserCreate,
		Read:   resourceUserRead,
		Update: resourceUserUpdate,
		Delete: resourceUserDelete,
		// Every attribute other than username, email and password is
		// Optional+Computed and only written when the configuration sets or
		// changes it. Before, they were plain Optional, but the SDK request
		// struct tags each field omitempty, so false and "" were never sent:
		// an omitted attribute showed a permanent diff (e.g. suspended
		// true -> false on a user suspended in the console) that apply never
		// carried out. Now that false and "" can be sent, keeping those
		// semantics would make that apply unsuspend the user; Computed makes
		// an omitted attribute mean "leave as is" instead.
		Schema: map[string]*schema.Schema{
			"username": {
				Type:     schema.TypeString,
				Required: true,
			},
			"email": {
				Type:     schema.TypeString,
				Required: true,
			},
			"firstname":    optionalComputedString("The user's first name. Example: `john`."),
			"lastname":     optionalComputedString("The user's last name. Example: `doe`."),
			"display_name": optionalComputedString("The user's display name. Example: `john doe`."),
			"password": {
				Description: "The user's password. Only sent to JumpCloud on create and when it " +
					"changes in the configuration; never read back. Removing it from the " +
					"configuration doesn't clear the password.",
				Type:      schema.TypeString,
				Optional:  true,
				Sensitive: true,
			},
			"enable_mfa":             optionalComputedBool("Require Multi-factor Authentication on the User Portal."),
			"ldap_binding_user":      optionalComputedBool("Allow the user to bind to JumpCloud LDAP."),
			"passwordless_sudo":      optionalComputedBool("Allow sudo without a password (requires sudo)."),
			"password_never_expires": optionalComputedBool("Exempt the user's password from expiry."),
			"sudo":                   optionalComputedBool("Grant sudo on the user's systems."),
			"suspended":              optionalComputedBool("Suspend the user."),
			"phone_number": {
				Description: "The user's phone numbers. Once set, the configured list replaces " +
					"the existing one." + userKeep,
				Type:     schema.TypeList,
				Optional: true,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"number": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringLenBetween(1, 1024),
						},
						"type": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice([]string{
								"mobile",
								"work",
								"work_fax",
							}, false),
						},
					},
				},
			},
			// Currently, only the options necessary for our use case are implemented
			// JumpCloud offers a lot more
		},
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
	}
}

// We receive a v2config from the TF base code but need a v1config to continue. So, we take the only
// preloaded element (the x-api-key) and populate the v1config with it.
//
// BasePath is derived from v2config's, not left at jcapiv1's own default: the
// two SDKs' real-world defaults differ only by a trailing "/v2"
// ("https://console.jumpcloud.com/api/v2" vs ".../api"), so trimming that
// suffix reproduces the real v1 base path in production while passing a
// test's mock server URL through unchanged (it has no such suffix to trim).
func convertV2toV1Config(v2config *jcapiv2.Configuration) *jcapiv1.Configuration {
	configv1 := jcapiv1.NewConfiguration()
	configv1.BasePath = strings.TrimSuffix(v2config.BasePath, "/v2")
	configv1.AddDefaultHeader("x-api-key", v2config.DefaultHeader["x-api-key"])
	if v2config.DefaultHeader["x-org-id"] != "" {
		configv1.AddDefaultHeader("x-org-id", v2config.DefaultHeader["x-org-id"])
	}
	return configv1
}

func resourceUserCreate(d *schema.ResourceData, m interface{}) error {
	configv1 := convertV2toV1Config(m.(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)

	var phoneNumbers []jcapiv1.SystemuserputpostPhoneNumbers
	phoneNumbersRaw, _ := json.Marshal(expandPhoneNumbers(d.Get("phone_number").([]interface{})))
	if err := json.Unmarshal(phoneNumbersRaw, &phoneNumbers); err != nil {
		return err
	}

	payload := jcapiv1.Systemuserputpost{
		Username:                    d.Get("username").(string),
		Email:                       d.Get("email").(string),
		Firstname:                   d.Get("firstname").(string),
		Lastname:                    d.Get("lastname").(string),
		Displayname:                 d.Get("display_name").(string),
		Password:                    d.Get("password").(string),
		EnableUserPortalMultifactor: d.Get("enable_mfa").(bool),
		LdapBindingUser:             d.Get("ldap_binding_user").(bool),
		Sudo:                        d.Get("sudo").(bool),
		PasswordlessSudo:            d.Get("passwordless_sudo").(bool),
		Suspended:                   d.Get("suspended").(bool),
		PasswordNeverExpires:        d.Get("password_never_expires").(bool),
		PhoneNumbers:                phoneNumbers,
	}
	req := map[string]interface{}{
		"body": payload,
	}
	returnstruc, _, err := client.SystemusersApi.SystemusersPost(context.TODO(),
		"", "", req)
	if err != nil {
		return err
	}
	d.SetId(returnstruc.Id)
	return resourceUserRead(d, m)
}

func resourceUserRead(d *schema.ResourceData, m interface{}) error {
	configv1 := convertV2toV1Config(m.(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)

	res, httpRes, err := client.SystemusersApi.SystemusersGet(context.TODO(),
		d.Id(), "", "", nil)

	// A user deleted outside Terraform answers 404 (historically also an empty
	// 200, surfacing as EOF): drop it from state so the plan re-creates it,
	// rather than failing every refresh.
	if err != nil {
		if isNotFound(httpRes, err) {
			log.Printf("[WARN] user %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}

	d.SetId(res.Id)

	if err := d.Set("username", res.Username); err != nil {
		return err
	}
	if err := d.Set("email", res.Email); err != nil {
		return err
	}
	if err := d.Set("firstname", res.Firstname); err != nil {
		return err
	}
	if err := d.Set("lastname", res.Lastname); err != nil {
		return err
	}
	if err := d.Set("display_name", res.Displayname); err != nil {
		return err
	}
	if err := d.Set("enable_mfa", res.EnableUserPortalMultifactor); err != nil {
		return err
	}
	if err := d.Set("ldap_binding_user", res.LdapBindingUser); err != nil {
		return err
	}
	if err := d.Set("password_never_expires", res.PasswordNeverExpires); err != nil {
		return err
	}
	if err := d.Set("sudo", res.Sudo); err != nil {
		return err
	}
	if err := d.Set("passwordless_sudo", res.PasswordlessSudo); err != nil {
		return err
	}
	if err := d.Set("suspended", res.Suspended); err != nil {
		return err
	}
	if err := d.Set("phone_number", flattenPhoneNumbers(res.PhoneNumbers)); err != nil {
		return err
	}

	return nil
}

func resourceUserUpdate(d *schema.ResourceData, m interface{}) error {
	configv1 := convertV2toV1Config(m.(*jcapiv2.Configuration))

	// A raw partial PUT of only the attributes that changed. The SDK's
	// Systemuserput tags every field omitempty, so false and "" could never be
	// sent through it (and it has no passwordless_sudo at all).
	// PUT /systemusers/{id} leaves omitted fields unchanged -- the provider has
	// always relied on that, since the SDK body never carried them.
	body := userUpdateBody(d)
	if len(body) == 0 {
		return resourceUserRead(d, m)
	}
	if _, err := jcWriteRaw(http.MethodPut, configv1.BasePath, configv1.DefaultHeader, "/systemusers/"+d.Id(), body); err != nil {
		return fmt.Errorf("error updating user %s: %s", d.Id(), err)
	}
	return resourceUserRead(d, m)
}

// userUpdateBody returns the changed attributes as a PUT /systemusers body.
func userUpdateBody(d *schema.ResourceData) map[string]interface{} {
	body := map[string]interface{}{}
	fields := map[string]string{
		"username":               "username",
		"email":                  "email",
		"firstname":              "firstname",
		"lastname":               "lastname",
		"display_name":           "displayname",
		"enable_mfa":             "enable_user_portal_multifactor",
		"ldap_binding_user":      "ldap_binding_user",
		"passwordless_sudo":      "passwordless_sudo",
		"password_never_expires": "password_never_expires",
		"sudo":                   "sudo",
		"suspended":              "suspended",
	}
	for attr, key := range fields {
		if d.HasChange(attr) {
			body[key] = d.Get(attr)
		}
	}
	// Only a new, non-empty password is sent: re-sending the current one on
	// every update re-submits it as a password change (which a password-history
	// policy can reject), and removing it from config isn't a request to clear it.
	if d.HasChange("password") {
		if pw := d.Get("password").(string); pw != "" {
			body["password"] = pw
		}
	}
	if d.HasChange("phone_number") {
		phones := []map[string]string{}
		for _, p := range expandPhoneNumbers(d.Get("phone_number").([]interface{})) {
			phones = append(phones, p)
		}
		body["phoneNumbers"] = phones
	}
	return body
}

func resourceUserDelete(d *schema.ResourceData, m interface{}) error {
	configv1 := convertV2toV1Config(m.(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)

	_, httpRes, err := client.SystemusersApi.SystemusersDelete(context.TODO(),
		d.Id(), "", headerAccept, nil)
	// A user that's already gone (e.g. deleted in the console) is the desired
	// end state, so don't fail -- or block -- destroy over a 404.
	if err != nil && !isNotFound(httpRes, err) {
		return fmt.Errorf("error deleting user %s: %s", d.Id(), err)
	}
	d.SetId("")
	return nil
}
