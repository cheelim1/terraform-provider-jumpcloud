package jumpcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	jcapiv1 "github.com/TheJumpCloud/jcapi-go/v1"
	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/go-resty/resty/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// ErrApplicationNotFound is returned by ApplicationGetRaw when the application
// has been deleted outside Terraform.
var ErrApplicationNotFound = errors.New("application not found")

// applicationRequestTimeout bounds every request made through
// applicationRawRequest. The raw helpers take no cancellation context, so
// without a timeout a server that accepts the connection but stalls would
// leave refresh/apply waiting forever while Terraform holds its state lock.
// A var so tests can shorten it.
var applicationRequestTimeout = 30 * time.Second

// applicationRawRequest builds a request for the raw application helpers below, carrying the
// provider configuration's default headers (x-api-key and, when configured,
// x-org-id) so raw calls are scoped to the same organization as SDK calls.
// Debug logging is deliberately off: resty's debug output includes request
// headers, which would write the x-api-key to the TF_LOG output.
func applicationRawRequest(headers map[string]string) *resty.Request {
	req := resty.New().SetTimeout(applicationRequestTimeout).R().SetHeader("Accept", "application/json")
	for k, v := range headers {
		if v != "" {
			req.SetHeader(k, v)
		}
	}
	return req
}

// ApplicationGetRaw fetches the full, raw JSON representation of an application.
// Needed because jcapiv1.ApplicationConfig only models a small subset of the real
// config object -- confirmed via a captured browser request, which showed fields
// (databaseAttributes rows, signAssertion, signResponse, declareRedirectEndpoint,
// includeGroups, groupsAttributeName, and more) that don't exist anywhere in the
// generated SDK types, so client.ApplicationsApi.ApplicationsGet silently drops
// them when unmarshaling into the typed struct.
func ApplicationGetRaw(basePath string, headers map[string]string, applicationId string) (map[string]interface{}, error) {
	resp, err := applicationRawRequest(headers).Get(basePath + "/applications/" + applicationId)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() == http.StatusNotFound {
		return nil, ErrApplicationNotFound
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error getting application %s: %s; body: %s",
			applicationId, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("error decoding application %s: %w", applicationId, err)
	}
	return result, nil
}

// applicationWriteRaw POSTs or PUTs a raw application body, for the same reason
// ApplicationGetRaw bypasses the generated client: the typed Application/
// ApplicationConfig structs can't represent the fields this resource now manages.
func applicationWriteRaw(method, basePath string, headers map[string]string, path string, body map[string]interface{}) (map[string]interface{}, error) {
	req := applicationRawRequest(headers).
		SetHeader("Content-Type", "application/json").
		SetBody(body)

	var resp *resty.Response
	var err error
	switch method {
	case http.MethodPost:
		resp, err = req.Post(basePath + path)
	case http.MethodPut:
		resp, err = req.Put(basePath + path)
	default:
		return nil, fmt.Errorf("applicationWriteRaw: unsupported method %s", method)
	}
	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error %s %s: %s; body: %s",
			method, path, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("error decoding response from %s %s: %w", method, path, err)
	}
	return result, nil
}

// ApplicationCreateRaw creates an application from a raw request body.
func ApplicationCreateRaw(basePath string, headers map[string]string, body map[string]interface{}) (map[string]interface{}, error) {
	return applicationWriteRaw(http.MethodPost, basePath, headers, "/applications", body)
}

// ApplicationUpdateRaw updates an application from a raw request body.
func ApplicationUpdateRaw(basePath string, headers map[string]string, applicationId string, body map[string]interface{}) (map[string]interface{}, error) {
	return applicationWriteRaw(http.MethodPut, basePath, headers, "/applications/"+applicationId, body)
}

const metadataXmlTimeout = 30 * time.Second

// isNotFound reports whether an SDK call failed because the object does not exist.
// The SDK returns the HTTP response alongside the error for non-2xx statuses; the "EOF"
// check is kept for the empty-body responses the API has historically returned.
func isNotFound(res *http.Response, err error) bool {
	if err == nil {
		return false
	}
	if res != nil && res.StatusCode == http.StatusNotFound {
		return true
	}
	return err.Error() == "EOF"
}

// Gets an application's metadata XML for SAML authentication
// this direct API call is a needed workaround since JumpCloud does not offer this endpoint through its SDK
func GetApplicationMetadataXml(orgId string, applicationId string, apiKey string) (string, error) {
	url := "https://console.jumpcloud.com/api/organizations/" + orgId + "/applications/" + applicationId + "/metadata.xml"

	// Debug logging is deliberately off: resty's debug output includes request headers,
	// which would write the x-api-key to the TF_LOG output.
	client := resty.New().SetTimeout(metadataXmlTimeout)

	resp, err := client.R().
		SetHeader("x-api-key", apiKey).
		Get(url)

	if err != nil {
		return "", err
	}

	log.Printf("[DEBUG] metadata XML for application %s: status=%s time=%s", applicationId, resp.Status(), resp.Time())

	// Without this check any non-2xx response body -- an auth failure, a 404, an HTML
	// error page -- is returned as if it were the metadata XML and stored in state.
	if resp.IsError() {
		return "", fmt.Errorf("error retrieving metadata XML for application %s: %s; body: %s",
			applicationId, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	return string(resp.Body()), nil
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func getUserGroupMemberIDs(client *jcapiv2.APIClient, groupID string) ([]string, error) {
	var userIds []string
	for i := 0; ; i++ {
		optionals := map[string]interface{}{
			"groupId": groupID,
			"limit":   int32(100),
			"skip":    int32(i * 100),
		}

		graphconnect, res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersList(
			context.TODO(), groupID, "", "", optionals)
		if err != nil {
			return nil, fmt.Errorf("error getting group members for group id %s, error:%s; response = %+v", groupID, err, res)
		}

		for _, v := range graphconnect {
			userIds = append(userIds, v.To.Id)
		}

		if len(graphconnect) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return userIds, nil
}

func userIDsToEmails(configv2 *jcapiv2.Configuration, userIDs []string) ([]string, error) {
	emails := make([]string, len(userIDs))

	if len(userIDs) == 0 {
		return emails, nil
	}

	configv1 := convertV2toV1Config(configv2)
	client := jcapiv1.NewAPIClient(configv1)

	for i := 0; ; i++ {
		users, res, err := client.SystemusersApi.SystemusersList(context.TODO(), "", "", map[string]interface{}{
			"filter": "_id:$in:" + strings.Join(userIDs[:], "|"),
			"limit":  int32(100),
			"skip":   int32(i * 100),
			"fields": "email",
			"sort":   "email",
		})

		if err != nil {
			return nil, fmt.Errorf("error loading user emails from IDs: %s, i:%d, error:%s; response:%+v", userIDs, i, err, res)
		}

		for j, result := range users.Results {
			emails[j+(i*100)] = result.Email
		}

		if len(users.Results) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return emails, nil
}

func userEmailsToIDs(configv2 *jcapiv2.Configuration, userEmailsInterface []interface{}) ([]string, error) {
	userEmails := make([]string, len(userEmailsInterface))
	for i, userEmail := range userEmailsInterface {
		userEmails[i] = userEmail.(string)
	}

	ids := make([]string, len(userEmailsInterface))

	if len(userEmails) == 0 {
		return ids, nil
	}

	configv1 := convertV2toV1Config(configv2)
	client := jcapiv1.NewAPIClient(configv1)

	for i := 0; ; i++ {
		users, res, err := client.SystemusersApi.SystemusersList(context.TODO(), "", "", map[string]interface{}{
			"filter": "email:$in:" + strings.Join(userEmails[:], "|"),
			"limit":  int32(100),
			"skip":   int32(i * 100),
			"fields": "_id",
			"sort":   "_id",
		})

		if err != nil {
			return nil, fmt.Errorf("error loading user IDs from emails:%s; response = %+v", err, res)
		}

		for j, result := range users.Results {
			ids[j+(i*100)] = result.Id
		}

		if len(users.Results) < 100 {
			break
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return ids, nil
}

func manageGroupMember(client *jcapiv2.APIClient, d *schema.ResourceData, memberID string, action string) error {
	payload := jcapiv2.UserGroupMembersReq{
		Op:    action,
		Type_: "user",
		Id:    memberID,
	}

	req := map[string]interface{}{
		"body": payload,
	}

	res, err := client.UserGroupMembersMembershipApi.GraphUserGroupMembersPost(
		context.TODO(), d.Id(), "", "", req)

	if err != nil {
		return fmt.Errorf("error managing group member, action: %s, member id:%s, error: %s; response = %+v", action, memberID, err, res)
	}
	return nil
}

// https://github.com/rootlyhq/terraform-provider-rootly/blob/99175a7ab4e154793ea8a8710d329a3f48eb0c90/tools/ignore_array_order.go#L12
func EqualIgnoringOrder(key, oldValue, newValue string, d *schema.ResourceData) bool {
	// The key is a path not the list itself, e.g. "events.0"
	lastDotIndex := strings.LastIndex(key, ".")
	if lastDotIndex != -1 {
		key = string(key[:lastDotIndex])
	}
	oldData, newData := d.GetChange(key)
	if oldData == nil || newData == nil {
		return false
	}
	oldArray := oldData.([]interface{})
	newArray := newData.([]interface{})
	if len(oldArray) != len(newArray) {
		// Items added or removed, always detect as changed
		return false
	}

	// Convert data to string arrays
	oldItems := make([]string, len(oldArray))
	newItems := make([]string, len(newArray))
	for i, oldItem := range oldArray {
		oldItems[i] = oldItem.(string)
	}
	for j, newItem := range newArray {
		newItems[j] = newItem.(string)
	}
	// Ensure consistent sorting before comparison, to suppress unnecessary change detections
	sort.Strings(oldItems)
	sort.Strings(newItems)
	return reflect.DeepEqual(oldItems, newItems)
}
