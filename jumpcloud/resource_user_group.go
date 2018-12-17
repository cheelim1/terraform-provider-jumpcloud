package jumpcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform/helper/schema"
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
			"xorgid": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"attributes": {
				Type:     schema.TypeMap,
				Optional: true,
				Computed: true,
				ForceNew: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"posix_groups": {
							Type:     schema.TypeString,
							Optional: true,
						},
					},
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

	body := jcapiv2.UserGroupPost{Name: d.Get("name").(string)}

	if attr, ok := expandAttributes(d.Get("attributes")); ok {
		body.Attributes = attr
	}

	req := map[string]interface{}{
		"body":   body,
		"xOrgId": d.Get("xorgid").(string),
	}
	group, res, err := client.UserGroupsApi.GroupsUserPost(context.TODO(), "", Accept, req)
	if err != nil {
		// TODO: sort out error essentials
		return fmt.Errorf("error creating user group %s: %s - response = %+v", (req["body"].(jcapiv2.UserGroupPost)).Name, err, res)
	}

	d.SetId(group.Id)
	return resourceUserGroupRead(d, m)
}

func resourceUserGroupRead(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)

	group, ok, err := trueUserGroupRead(config, d.Id())
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

	return nil
}

func trueUserGroupRead(config *jcapiv2.Configuration, id string) (ug *UserGroup, ok bool, err error) {
	req, err := http.NewRequest(http.MethodGet, config.BasePath+"/usergroups/"+id, nil)
	if err != nil {
		return
	}

	req.Header.Add("x-api-key", config.DefaultHeader["x-api-key"])
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return
	}

	ok = true
	err = json.NewDecoder(res.Body).Decode(&ug)
	return
}

func resourceUserGroupUpdate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)

	body := jcapiv2.UserGroupPost{Name: d.Get("name").(string)}
	if attr, ok := expandAttributes(d.Get("attributes")); ok {
		body.Attributes = attr
	} else {
		return errors.New("unable to update, attributes not expandable")
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPut, config.BasePath+"/usergroups/"+d.Id(), bytes.NewBuffer(b))
	if err != nil {
		return err
	}

	req.Header.Add("x-api-key", config.DefaultHeader["x-api-key"])
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	_, err = http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	// TODO: HTTP errors, generic request func?
	return resourceUserGroupRead(d, m)
}

func resourceUserGroupDelete(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	res, err := client.UserGroupsApi.GroupsUserDelete(context.TODO(), d.Id(), "", Accept, nil)
	if err != nil {
		// TODO: sort out error essentials
		return fmt.Errorf("error deleting user group: %s - response = %+v", err, res)
	}
	d.SetId("")
	return nil
}