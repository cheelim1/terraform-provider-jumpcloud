package jumpcloud

import (
	"context"
	"fmt"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

func dataSourceJumpCloudUserGroup() *schema.Resource {
	return &schema.Resource{
		Read: dataSourceJumpCloudUserGroupRead,
		Schema: map[string]*schema.Schema{
			"group_name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"members": {
				Type:        schema.TypeList,
				Computed:    true,
				Description: "This is a set of user emails associated with this group",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
		},
	}
}

func dataSourceJumpCloudUserGroupRead(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	groupName := d.Get("group_name").(string)

	// The filter must be a []string ("name:eq:<name>"): the SDK silently drops
	// any other type, which left this listing unfiltered and, without paging,
	// limited to the API's first page of groups. Page through the results and
	// match the name exactly, so a filter mismatch can't pick the wrong group.
	for i := 0; i < maxGraphPages; i++ {
		groups, _, err := client.UserGroupsApi.GroupsUserList(context.Background(), "application/json", "application/json", map[string]interface{}{
			"filter": []string{"name:eq:" + groupName},
			"limit":  int32(100),
			"skip":   int32(i * 100),
		})
		if err != nil {
			return err
		}

		for _, group := range groups {
			if group.Name == groupName {
				d.SetId(group.Id)

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
		}
		if len(groups) < 100 {
			break
		}
	}

	return fmt.Errorf("No user group found with name: %s", groupName)
}
