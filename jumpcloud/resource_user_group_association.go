package jumpcloud

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

func resourceUserGroupAssociation() *schema.Resource {
	return &schema.Resource{
		Description: "Provides a resource for associating a JumpCloud user group to objects like SSO applications, G Suite, Office 365, LDAP, and more.",
		Create:      resourceUserGroupAssociationCreate,
		Read:        resourceUserGroupAssociationRead,
		Update:      nil,
		Delete:      resourceUserGroupAssociationDelete,
		Importer: &schema.ResourceImporter{
			State: resourceUserGroupAssociationImport,
		},
		Schema: map[string]*schema.Schema{
			"group_id": {
				Description: "The ID of the `resource_user_group` resource.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"object_id": {
				Description: "The ID of the object to associate with the group.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"type": {
				Description: "The type of the object to associate with the given group. Possible values: `active_directory`, `application`, `command`, `g_suite`, `ldap_server`, `office_365`, `policy`, `radius_server`, `system`, `system_group`.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				ValidateFunc: func(val interface{}, key string) (warns []string, errors []error) {
					allowedValues := []string{
						"active_directory",
						"application",
						"command",
						"g_suite",
						"ldap_server",
						"office_365",
						"policy",
						"radius_server",
						"system",
						"system_group",
					}

					v := val.(string)
					if !stringInSlice(v, allowedValues) {
						errors = append(errors, fmt.Errorf("%q must be one of %q", key, allowedValues))
					}
					return
				},
			},
		},
	}
}

func modifyUserGroupAssociation(client *jcapiv2.APIClient, d *schema.ResourceData, action string) diag.Diagnostics {
	_, err := postUserGroupAssociation(client, d, action)
	return diag.FromErr(err)
}

// postUserGroupAssociation adds or removes the association, returning the HTTP
// response so callers can tell a 404 apart from other failures.
func postUserGroupAssociation(client *jcapiv2.APIClient, d *schema.ResourceData, action string) (*http.Response, error) {
	payload := jcapiv2.UserGroupGraphManagementReq{
		Op:    action,
		Type_: d.Get("type").(string),
		Id:    d.Get("object_id").(string),
	}

	req := map[string]interface{}{
		"body": payload,
	}

	return client.UserGroupAssociationsApi.GraphUserGroupAssociationsPost(
		context.TODO(), d.Get("group_id").(string), "", "", req)
}

func resourceUserGroupAssociationCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	diags := modifyUserGroupAssociation(client, d, "add")
	if diags.HasError() {
		return fmt.Errorf("Error creating user group association: %v", diags)
	}

	// Set the resource ID
	d.SetId(fmt.Sprintf("%s/%s/%s", d.Get("group_id").(string), d.Get("object_id").(string), d.Get("type").(string)))

	return resourceUserGroupAssociationRead(d, meta)
}

func resourceUserGroupAssociationRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	// Retrieve the group_id, object_id, and type from the resource data
	groupID := d.Get("group_id").(string)
	objectID := d.Get("object_id").(string)
	objectType := d.Get("type").(string)

	attempts := 1
	if d.IsNewResource() {
		// Right after Create the new edge may not be listed yet.
		attempts = newEdgeReadRetries
	}
	for attempt := 1; ; attempt++ {
		found, err := userGroupAssociationExists(client, groupID, objectID, objectType)
		if err != nil {
			return err
		}
		if found {
			d.SetId(fmt.Sprintf("%s/%s/%s", groupID, objectID, objectType))
			return nil
		}
		if attempt >= attempts {
			break
		}
		time.Sleep(newEdgeReadDelay)
	}

	if d.IsNewResource() {
		return fmt.Errorf("association of %s %s with user group %s not found after adding it", objectType, objectID, groupID)
	}
	// Removed outside Terraform (or the group was deleted): unset the ID so
	// the plan re-adds it.
	d.SetId("")
	return nil
}

// userGroupAssociationExists pages through every association of objectType:
// one beyond the first page (100) must still be found, or it would be planned
// for re-adding forever. A group that no longer exists has none.
func userGroupAssociationExists(client *jcapiv2.APIClient, groupID, objectID, objectType string) (bool, error) {
	for i := 0; i < maxGraphPages; i++ {
		optionals := map[string]interface{}{
			"groupId": groupID,
			"limit":   int32(100),
			"skip":    int32(i * 100),
		}
		graphConnect, res, err := client.UserGroupAssociationsApi.GraphUserGroupAssociationsList(
			context.TODO(), groupID, "", "", []string{objectType}, optionals)
		if isNotFound(res, err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		for _, v := range graphConnect {
			if v.To.Id == objectID {
				return true, nil
			}
		}
		if len(graphConnect) < 100 {
			break
		}
	}
	return false, nil
}

func resourceUserGroupAssociationDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	res, err := postUserGroupAssociation(client, d, "remove")
	// Already removed (e.g. in the console) is the desired end state.
	if err != nil && !isNotFound(res, err) {
		return fmt.Errorf("Error deleting user group association: %v", err)
	}
	return nil
}

func resourceUserGroupAssociationImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	// Expected ID format: <group_id>/<object_id>/<type>
	idParts := strings.Split(d.Id(), "/")
	if len(idParts) != 3 {
		return nil, fmt.Errorf("unexpected format of ID (%s), expected <group_id>/<object_id>/<type>", d.Id())
	}
	groupID := idParts[0]
	objectID := idParts[1]
	objectType := idParts[2]

	// Set the parsed values into the resource data
	if err := d.Set("group_id", groupID); err != nil {
		return nil, err
	}
	if err := d.Set("object_id", objectID); err != nil {
		return nil, err
	}
	if err := d.Set("type", objectType); err != nil {
		return nil, err
	}

	// Set the ID again to ensure consistency
	d.SetId(fmt.Sprintf("%s/%s/%s", groupID, objectID, objectType))

	return []*schema.ResourceData{d}, nil
}
