package jumpcloud

import (
	"context"
	"fmt"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	// "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourceJumpCloudUser() *schema.Resource {
	return &schema.Resource{
		Read: dataSourceJumpCloudUserRead,
		Schema: map[string]*schema.Schema{
			"email": {
				Type:     schema.TypeString,
				Required: true,
			},
			"username": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func getUserDetails(client *jcapiv1.APIClient, email string) (*jcapiv1.Systemuserreturn, error) {
	ctx := context.TODO()

	contentType := "application/json"
	accept := "application/json"

	// Built with encoding/json, not by string formatting: an email containing
	// a quote or backslash produced invalid JSON, the unmarshal error was
	// ignored, and the search then ran with no filter at all -- returning the
	// first user in the org.
	var filter interface{} = []map[string]string{{"email": email}}

	optionals := map[string]interface{}{
		"body": jcapiv1.Search{
			Filter: &filter,
		},
	}

	res, _, err := client.SearchApi.SearchSystemusersPost(ctx, contentType, accept, optionals)
	if err != nil {
		return nil, err
	}

	// Check if user is found
	if len(res.Results) == 0 {
		return nil, fmt.Errorf("no user found with the given email: %s", email)
	}

	// Return the first user found
	user := res.Results[0]

	return &user, nil
}

func dataSourceJumpCloudUserRead(d *schema.ResourceData, m interface{}) error {
	configv1 := convertV2toV1Config(m.(*jcapiv2.Configuration))
	client := jcapiv1.NewAPIClient(configv1)
	userEmail := d.Get("email").(string)

	// Use the getUserDetails function to query user details using the userEmail
	user, err := getUserDetails(client, userEmail)
	if err != nil {
		// Returned as is: an auth failure or outage isn't "user not found".
		return err
	}

	// Set the user ID in the Terraform resource data object
	d.SetId(user.Id)
	if err := d.Set("username", user.Username); err != nil {
		return err
	}

	return nil
}
