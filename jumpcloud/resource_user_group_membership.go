package jumpcloud

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

func resourceUserGroupMembership() *schema.Resource {
	return &schema.Resource{
		Description: "Provides a resource for managing user group memberships.",
		Create:      resourceUserGroupMembershipCreate,
		Read:        resourceUserGroupMembershipRead,
		Update:      nil, // No update routine, as association cannot be updated
		Delete:      resourceUserGroupMembershipDelete,
		Schema: map[string]*schema.Schema{
			"userid": {
				Description: "The ID of the `resource_user` object.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
			"groupid": {
				Description: "The ID of the `resource_user_group` object.",
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
			},
		},
		Importer: &schema.ResourceImporter{
			State: userGroupMembershipImporter,
		},
	}
}

func userGroupMembershipImporter(d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
	ids := strings.Split(d.Id(), "/")
	if len(ids) != 2 {
		return nil, fmt.Errorf("Invalid import format. Expected 'groupid/userid'")
	}
	groupID, userID := ids[0], ids[1]

	_ = d.Set("groupid", groupID)
	_ = d.Set("userid", userID)

	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	isMember, err := checkUserGroupMembership(client, groupID, userID)
	if err != nil {
		return nil, err
	}

	if isMember {
		d.SetId(groupID + "/" + userID)
		return []*schema.ResourceData{d}, nil
	}

	return nil, fmt.Errorf("User %s is not a member of group %s", userID, groupID)
}

// maxGraphPages bounds the graph list pagination loops (100 per page) so an
// API that ignored skip couldn't loop forever; 100,000 edges is far beyond any
// real group.
const maxGraphPages = 1000

// membershipReadRetries/Delay give a just-added membership time to become
// visible (the graph is eventually consistent) before Create gives up. Vars
// so tests can shorten them.
var (
	membershipReadRetries = 5
	membershipReadDelay   = time.Second
)

func checkUserGroupMembership(client *jcapiv2.APIClient, groupID, userID string) (bool, error) {
	for i := 0; i < maxGraphPages; i++ {
		optionals := map[string]interface{}{
			"groupId": groupID,
			"limit":   int32(100),
			"skip":    int32(i * 100),
		}

		graphconnect, _, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersList(
			context.TODO(), groupID, "", "", optionals)
		if err != nil {
			return false, err
		}

		for _, v := range graphconnect {
			if v.To.Id == userID {
				return true, nil
			}
		}

		if len(graphconnect) < 100 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, nil
}

func modifyUserGroupMembership(client *jcapiv2.APIClient,
	d *schema.ResourceData, action string) (*http.Response, error) {

	payload := jcapiv2.UserGroupMembersReq{
		Op:    action,
		Type_: "user",
		Id:    d.Get("userid").(string),
	}

	req := map[string]interface{}{
		"body": payload,
	}

	return client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
		context.TODO(), d.Get("groupid").(string), "", "", req)
}

func resourceUserGroupMembershipCreate(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)

	if _, err := modifyUserGroupMembership(client, d, "add"); err != nil {
		return err
	}
	// The membership exists now: record it, so a failed read-back below can't
	// leave it in JumpCloud untracked.
	d.SetId(d.Get("groupid").(string) + "/" + d.Get("userid").(string))
	return resourceUserGroupMembershipRead(d, m)
}

func resourceUserGroupMembershipRead(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)
	groupID, userID := d.Get("groupid").(string), d.Get("userid").(string)

	attempts := 1
	if d.IsNewResource() {
		// Right after Create the new edge may not be listed yet.
		attempts = membershipReadRetries
	}
	for attempt := 1; ; attempt++ {
		found, err := checkUserGroupMembership(client, groupID, userID)
		if err != nil {
			return err
		}
		if found {
			// As we not have a JC-ID for the membership we simply store
			// the concatenation of group ID and user ID as our membership ID
			d.SetId(groupID + "/" + userID)
			return nil
		}
		if attempt >= attempts {
			break
		}
		time.Sleep(membershipReadDelay)
	}

	if d.IsNewResource() {
		return fmt.Errorf("User ID %s not found in group ID %s after adding it", userID, groupID)
	}
	// Removed outside Terraform (or the user or group was deleted): drop it
	// from state so the plan re-adds it, instead of failing every refresh.
	log.Printf("[WARN] user %s is no longer a member of group %s, removing from state", userID, groupID)
	d.SetId("")
	return nil
}

func resourceUserGroupMembershipDelete(d *schema.ResourceData, m interface{}) error {
	config := m.(*jcapiv2.Configuration)
	client := jcapiv2.NewAPIClient(config)
	res, err := modifyUserGroupMembership(client, d, "remove")
	// Already removed (e.g. in the console) is the desired end state.
	if err != nil && !isNotFound(res, err) {
		return err
	}
	return nil
}
