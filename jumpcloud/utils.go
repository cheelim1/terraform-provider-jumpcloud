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

// ErrNotFound is returned by the Get/Delete raw helpers below when the
// underlying object has been deleted outside Terraform.
var ErrNotFound = errors.New("not found")

// jcRawRequest builds a request for the raw helpers below, carrying the
// provider configuration's default headers (x-api-key and, when configured,
// x-org-id) so raw calls are scoped to the same organization as SDK calls.
// Debug logging is deliberately off: resty's debug output includes request
// headers, which would write the x-api-key to the TF_LOG output.
func jcRawRequest(headers map[string]string) *resty.Request {
	req := resty.New().R().SetHeader("Accept", "application/json")
	for k, v := range headers {
		if v != "" {
			req.SetHeader(k, v)
		}
	}
	return req
}

// jcGetRaw fetches a raw JSON object from a v1 or v2 JumpCloud endpoint.
func jcGetRaw(basePath string, headers map[string]string, path string) (map[string]interface{}, error) {
	resp, err := jcRawRequest(headers).Get(basePath + path)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error getting %s: %s; body: %s",
			path, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("error decoding response from %s: %w", path, err)
	}
	return result, nil
}

// jcListRaw fetches the "results" array from a v1 list endpoint, as a slice of
// raw JSON objects.
func jcListRaw(basePath string, headers map[string]string, path string) ([]map[string]interface{}, error) {
	resp, err := jcRawRequest(headers).Get(basePath + path)
	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error listing %s: %s; body: %s",
			path, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	var decoded struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(resp.Body(), &decoded); err != nil {
		return nil, fmt.Errorf("error decoding response from %s: %w", path, err)
	}
	return decoded.Results, nil
}

// jcListArrayRaw fetches a v2 list endpoint that returns a bare JSON array
// directly (confirmed live for /push/configs and /webauthn/configs), unlike
// v1 list endpoints (jcListRaw), which wrap results in a "results" key.
func jcListArrayRaw(basePath string, headers map[string]string, path string) ([]map[string]interface{}, error) {
	resp, err := jcRawRequest(headers).Get(basePath + path)
	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error listing %s: %s; body: %s",
			path, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &decoded); err != nil {
		return nil, fmt.Errorf("error decoding response from %s: %w", path, err)
	}
	return decoded, nil
}

// jcWriteRaw POSTs, PUTs, or DELETEs a raw JSON body against a v1 or v2
// JumpCloud endpoint. Some V2 endpoints answer a successful write with 204 and
// an empty body, so an empty response body is treated as success rather than
// a decode error.
func jcWriteRaw(method, basePath string, headers map[string]string, path string, body map[string]interface{}) (map[string]interface{}, error) {
	req := jcRawRequest(headers).SetHeader("Content-Type", "application/json")
	if body != nil {
		req = req.SetBody(body)
	}

	var resp *resty.Response
	var err error
	switch method {
	case http.MethodPost:
		resp, err = req.Post(basePath + path)
	case http.MethodPut:
		resp, err = req.Put(basePath + path)
	case http.MethodPatch:
		resp, err = req.Patch(basePath + path)
	case http.MethodDelete:
		resp, err = req.Delete(basePath + path)
	default:
		return nil, fmt.Errorf("jcWriteRaw: unsupported method %s", method)
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.IsError() {
		return nil, fmt.Errorf("error %s %s: %s; body: %s",
			method, path, resp.Status(), strings.TrimSpace(string(resp.Body())))
	}

	respBody := strings.TrimSpace(string(resp.Body()))
	if respBody == "" {
		return nil, nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("error decoding response from %s %s: %w", method, path, err)
	}
	return result, nil
}

// OrganizationsListRaw lists organizations visible to this API key (v1).
func OrganizationsListRaw(basePath string, headers map[string]string) ([]map[string]interface{}, error) {
	return jcListRaw(basePath, headers, "/organizations")
}

// OrganizationGetRaw fetches the full, raw JSON representation of an
// organization (v1), including its settings object.
func OrganizationGetRaw(basePath string, headers map[string]string, orgId string) (map[string]interface{}, error) {
	return jcGetRaw(basePath, headers, "/organizations/"+orgId)
}

// OrganizationUpdateRaw updates an organization (v1) from a raw request body.
func OrganizationUpdateRaw(basePath string, headers map[string]string, orgId string, body map[string]interface{}) (map[string]interface{}, error) {
	result, err := jcWriteRaw(http.MethodPut, basePath, headers, "/organizations/"+orgId, body)
	if err != nil {
		return nil, err
	}
	if result == nil {
		// Some organization settings writes answer with 204/empty body; the
		// caller re-reads via OrganizationGetRaw afterward regardless.
		return map[string]interface{}{}, nil
	}
	return result, nil
}

// AuthPolicyGetRaw fetches the full, raw JSON representation of an
// authentication policy (v2).
func AuthPolicyGetRaw(basePath string, headers map[string]string, id string) (map[string]interface{}, error) {
	return jcGetRaw(basePath, headers, "/authn/policies/"+id)
}

// AuthPolicyCreateRaw creates an authentication policy (v2) from a raw request body.
func AuthPolicyCreateRaw(basePath string, headers map[string]string, body map[string]interface{}) (map[string]interface{}, error) {
	return jcWriteRaw(http.MethodPost, basePath, headers, "/authn/policies", body)
}

// AuthPolicyUpdateRaw updates an authentication policy (v2) from a raw request body.
func AuthPolicyUpdateRaw(basePath string, headers map[string]string, id string, body map[string]interface{}) (map[string]interface{}, error) {
	// Confirmed live via a captured browser request: this endpoint uses PATCH,
	// not PUT (PUT 404s). jc-cli's generic V2Client.Update always uses PUT,
	// which is wrong for this specific endpoint -- another case of jc-cli's
	// own assumptions not holding for this unverified area.
	result, err := jcWriteRaw(http.MethodPatch, basePath, headers, "/authn/policies/"+id, body)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]interface{}{}, nil
	}
	return result, nil
}

// AuthPolicyDeleteRaw deletes an authentication policy (v2).
func AuthPolicyDeleteRaw(basePath string, headers map[string]string, id string) error {
	_, err := jcWriteRaw(http.MethodDelete, basePath, headers, "/authn/policies/"+id, nil)
	return err
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
