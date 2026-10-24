package jumpcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"net/http"
	"slices"
)

func resourceUserGroup() *schema.Resource {
	return &schema.Resource{
		Create: resourceUserGroupCreate,
		Read:   resourceUserGroupRead,
		Update: resourceUserGroupUpdate,
		Delete: resourceUserGroupDelete,
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"attributes": {
				Type:     schema.TypeMap,
				Optional: true,
				Computed: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"posix_groups": {
							Type: schema.TypeString,
							// PosixGroups cannot be edited after group creation.
							ForceNew: true,
							Optional: true,
						},
						// enable_samba has a more complicated lifecycle,
						// Commenting out for now as it is ignored in CRU by the JCAPI
						// From Jumpcloud UI:
						// Samba Authentication must be configured in the
						// JumpCloud LDAP Directory and LDAP sync must be enabled
						// on this group before Samba Authentication can be enabled.
						// "enable_samba": {
						// 	Type:     schema.TypeBool,
						// 	Optional: true,
						// },
					},
				},
			},
			"custom_attributes": {
				Type:     schema.TypeMap,
				Optional: true,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Description: "String-valued custom attributes on the group, which members inherit " +
					"(e.g. for SSO attribute mappings). When set, this is authoritative over the " +
					"group's string-valued custom attributes; non-string ones set in the console " +
					"are left untouched.",
			},
			"members": {
				Type:             schema.TypeList,
				Optional:         true,
				Computed:         true,
				DiffSuppressFunc: EqualIgnoringOrder,
				Description:      "This is a set of user emails associated with this group",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
		},
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
	}
}

func resourceUserGroupCreate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	// Raw request rather than the SDK: jcapi-go's UserGroupAttributes model has
	// no room for custom attributes, which live as plain keys on "attributes".
	attrs := map[string]interface{}{}
	// For Attributes.PosixGroups, only the first member of the slice
	// is considered by the JCAPI
	if attr, ok := expandAttributes(d.Get("attributes")); ok {
		attrs["posixGroups"] = attr.PosixGroups
	}
	applyCustomAttributes(attrs, nil, d.Get("custom_attributes").(map[string]interface{}))

	body := map[string]interface{}{
		"name":       d.Get("name").(string),
		"attributes": attrs,
	}
	group, err := jcWriteRaw(http.MethodPost, config.BasePath, config.DefaultHeader, "/usergroups", body)
	if err != nil {
		return fmt.Errorf("error creating user group %s: %s", body["name"], err)
	}

	id, _ := group["id"].(string)
	d.SetId(id)

	memberIds, err := userEmailsToIDs(config, d.Get("members").([]interface{}))
	if err != nil {
		return err
	}

	for _, memberId := range memberIds {
		err := manageGroupMember(client, d, memberId, "add")
		if err != nil {
			return err
		}
	}
	return resourceUserGroupRead(d, m)
}

// resourceUserGroupRead uses a helper function that consumes the
// JC's HTTP API directly; the groups' attributes need to be kept in state
// as they are required for resourceUserGroupUpdate and the current
// implementation of the JC SDK doesn't support their retrieval
func resourceUserGroupRead(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)

	group, ok, err := userGroupReadHelper(config, d.Id())
	if err != nil {
		return err
	}

	if !ok {
		// not found
		d.SetId("")
		return nil
	}

	d.SetId(group.ID)
	if err := d.Set("name", group.Name); err != nil {
		return err
	}
	if err := d.Set("attributes", flattenAttributes(&group.Attributes)); err != nil {
		return err
	}
	if err := d.Set("custom_attributes", flattenCustomAttributes(group.RawAttributes)); err != nil {
		return err
	}

	client := jcapiv2.NewAPIClient(config)
	memberIDs, err := getUserGroupMemberIDs(client, d.Id())
	if err != nil {
		return err
	}
	memberEmails, err := userIDsToEmails(config, memberIDs)
	if err != nil {
		return err
	}
	if err := d.Set("members", memberEmails); err != nil {
		return err
	}

	return nil
}

// userGroupReadHelper fetches a group through the shared raw helpers, which
// send every configured default header, bound the request with a timeout, and
// return an error for any non-2xx response. Only a 404 means the group is gone
// (ok == false); every other failure -- auth, outage, malformed body -- is an
// error, so a failed refresh can't be mistaken for an empty or deleted group.
func userGroupReadHelper(config *jcapiv2.Configuration, id string) (ug *UserGroup,
	ok bool, err error) {

	raw, err := jcGetRaw(config.BasePath, config.DefaultHeader, "/usergroups/"+id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, false, err
	}
	ug = &UserGroup{}
	if err := json.Unmarshal(encoded, ug); err != nil {
		return nil, false, fmt.Errorf("error decoding user group %s: %w", id, err)
	}
	// The typed decode drops custom attributes, which jcapi-go's model doesn't
	// know about; keep the untyped attributes object too.
	ug.RawAttributes, _ = raw["attributes"].(map[string]interface{})
	return ug, true, nil
}

func resourceUserGroupUpdate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	// JumpCloud's PATCH /usergroups/{id} behaves like a PUT: confirmed live, it
	// clears any top-level field (e.g. description) or attributes key (e.g. a
	// custom attribute) the request omits. So start from the group's full
	// current state and change only the fields this resource manages.
	body, err := jcGetRaw(config.BasePath, config.DefaultHeader, "/usergroups/"+d.Id())
	if err != nil {
		return fmt.Errorf("error reading user group %s before update: %s", d.Id(), err)
	}
	for _, k := range []string{"id", "type", "organizationObjectId", "memberQueryErrorFlags", "suggestionCounts"} {
		delete(body, k)
	}

	body["name"] = d.Get("name").(string)

	attrs, ok := body["attributes"].(map[string]interface{})
	if !ok {
		attrs = map[string]interface{}{}
		body["attributes"] = attrs
	}
	// posix_groups is ForceNew, so it never changes here; whatever the server
	// has is carried over by the read-modify-write.
	oldCustom, newCustom := d.GetChange("custom_attributes")
	applyCustomAttributes(attrs, oldCustom.(map[string]interface{}), newCustom.(map[string]interface{}))

	if _, err := jcWriteRaw(http.MethodPatch, config.BasePath, config.DefaultHeader, "/usergroups/"+d.Id(), body); err != nil {
		return fmt.Errorf("error updating user group %s: %s", d.Id(), err)
	}

	oldMemberIDs, err := getUserGroupMemberIDs(client, d.Id())
	if err != nil {
		return err
	}

	newMemberIDs, err := userEmailsToIDs(config, d.Get("members").([]interface{}))
	if err != nil {
		return err
	}

	//add any new users
	for _, newMemberID := range newMemberIDs {
		if !slices.Contains(oldMemberIDs, newMemberID) {
			err := manageGroupMember(client, d, newMemberID, "add")
			if err != nil {
				return err
			}
		}
	}

	//remove any old users
	for _, oldMemberID := range oldMemberIDs {
		if !slices.Contains(newMemberIDs, oldMemberID) {
			err := manageGroupMember(client, d, oldMemberID, "remove")
			if err != nil {
				return err
			}
		}
	}

	return resourceUserGroupRead(d, m)
}

func resourceUserGroupDelete(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	res, err := client.UserGroupsApi.GroupsUserDelete(context.TODO(),
		d.Id(), "", headerAccept, nil)
	if err != nil {
		// TODO: sort out error essentials
		return fmt.Errorf("error deleting user group:%s; response = %+v", err, res)
	}
	d.SetId("")
	return nil
}
