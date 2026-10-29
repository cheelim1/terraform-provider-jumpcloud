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

// rawRequestTimeout bounds every request made through jcRawRequest. The raw
// helpers take no cancellation context, so without a timeout a server that
// accepts the connection but stalls would leave refresh/apply waiting forever
// while Terraform holds its state lock. A var so tests can shorten it.
var rawRequestTimeout = 30 * time.Second

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
	req := resty.New().SetTimeout(rawRequestTimeout).R().SetHeader("Accept", "application/json")
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

// withoutOrgHeader returns a copy of headers without x-org-id. The
// /organizations/{id} endpoints already name the organization in the path, and
// JumpCloud rejects a request that selects one a second time via the header
// ("403 Forbidden: organization selection not allowed via header"). It copies
// rather than deleting in place because the map is the provider's shared
// default headers, which every other resource still needs x-org-id from.
func withoutOrgHeader(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if !strings.EqualFold(k, "x-org-id") {
			out[k] = v
		}
	}
	return out
}

// OrganizationGetRaw fetches the full, raw JSON representation of an
// organization (v1), including its settings object. It never sends x-org-id;
// see withoutOrgHeader.
func OrganizationGetRaw(basePath string, headers map[string]string, orgId string) (map[string]interface{}, error) {
	return jcGetRaw(basePath, withoutOrgHeader(headers), "/organizations/"+orgId)
}

// OrganizationUpdateRaw updates an organization (v1) from a raw request body.
// It never sends x-org-id; see withoutOrgHeader.
func OrganizationUpdateRaw(basePath string, headers map[string]string, orgId string, body map[string]interface{}) (map[string]interface{}, error) {
	result, err := jcWriteRaw(http.MethodPut, basePath, withoutOrgHeader(headers), "/organizations/"+orgId, body)
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

// discoverSingletonConfigId lists a v2 config collection that is expected to
// hold exactly one object per organization and returns that object's id,
// trying idKeys in order. Like organization discovery, it refuses to guess
// when the list holds several objects: list order isn't a selection contract,
// and writing to the wrong config would silently change the wrong settings.
func discoverSingletonConfigId(config *jcapiv2.Configuration, path, name string, idKeys ...string) (string, error) {
	configs, err := jcListArrayRaw(config.BasePath, config.DefaultHeader, path)
	if err != nil {
		return "", err
	}
	if len(configs) == 0 {
		return "", fmt.Errorf("no %s configs visible to this API key; set config_id explicitly", name)
	}
	if len(configs) > 1 {
		return "", fmt.Errorf("GET %s returned %d %s configs; set config_id explicitly to choose one",
			path, len(configs), name)
	}
	for _, k := range idKeys {
		if id, _ := configs[0][k].(string); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not determine %s config id from %s response", name, path)
}

// Gets an application's metadata XML for SAML authentication
// this direct API call is a needed workaround since JumpCloud does not offer this endpoint through its SDK
// basePathV1 is the v1 API base path (e.g. https://console.jumpcloud.com/api,
// what convertV2toV1Config derives from the provider's configuration).
func GetApplicationMetadataXml(basePathV1, orgId string, applicationId string, apiKey string) (string, error) {
	url := applicationMetadataXmlURL(basePathV1, orgId, applicationId)

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

// applicationMetadataXmlURL is the SAML metadata URL for an application. It's
// built from the configured base path rather than a hard-coded host, so it
// follows the provider's configuration (and tests' fake servers); with the
// default configuration it is exactly
// https://console.jumpcloud.com/api/organizations/{org}/applications/{id}/metadata.xml.
func applicationMetadataXmlURL(basePathV1, orgId, applicationId string) string {
	return basePathV1 + "/organizations/" + orgId + "/applications/" + applicationId + "/metadata.xml"
}

// stringOr returns v as a string, or def when v is absent, null, or not a string.
func stringOr(v interface{}, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// boolOr returns v as a bool, or def when v is absent, null, or not a bool.
func boolOr(v interface{}, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

// intOr returns v as an int, or def when v is absent, null, or not a number.
// encoding/json decodes every JSON number into a float64.
func intOr(v interface{}, def int) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return def
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

// listSystemUsers pages through GET /systemusers with the given filter,
// fields and sort, 100 at a time.
func listSystemUsers(client *jcapiv1.APIClient, filter, fields, sortBy string) ([]jcapiv1.Systemuserreturn, error) {
	var out []jcapiv1.Systemuserreturn
	for i := 0; i < maxGraphPages; i++ {
		users, res, err := client.SystemusersApi.SystemusersList(context.TODO(), "", "", map[string]interface{}{
			"filter": filter,
			"limit":  int32(100),
			"skip":   int32(i * 100),
			"fields": fields,
			"sort":   sortBy,
		})
		if err != nil {
			return nil, fmt.Errorf("error listing users (filter %s): %s; response = %+v", filter, err, res)
		}
		out = append(out, users.Results...)
		if len(users.Results) < 100 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return out, nil
}

// userIDsToEmails maps user ids to their emails. An id with no user (deleted)
// is skipped rather than returned as an empty email, which would show up as a
// "" member and a permanent diff.
func userIDsToEmails(configv2 *jcapiv2.Configuration, userIDs []string) ([]string, error) {
	emails := []string{}
	if len(userIDs) == 0 {
		return emails, nil
	}

	client := jcapiv1.NewAPIClient(convertV2toV1Config(configv2))
	users, err := listSystemUsers(client, "_id:$in:"+strings.Join(userIDs, "|"), "email", "email")
	if err != nil {
		return nil, fmt.Errorf("error loading user emails from IDs %s: %s", userIDs, err)
	}
	for _, u := range users {
		if u.Email != "" {
			emails = append(emails, u.Email)
		}
	}
	return emails, nil
}

// userEmailsToIDs maps member emails to user ids. An email that matches no
// JumpCloud user is an error naming it; before, it became an empty id that
// the membership API then rejected with an unhelpful message.
func userEmailsToIDs(configv2 *jcapiv2.Configuration, userEmailsInterface []interface{}) ([]string, error) {
	ids := []string{}
	unique := []string{}
	seen := map[string]bool{}
	for _, e := range userEmailsInterface {
		email := e.(string)
		if !seen[strings.ToLower(email)] {
			seen[strings.ToLower(email)] = true
			unique = append(unique, email)
		}
	}
	if len(unique) == 0 {
		return ids, nil
	}

	client := jcapiv1.NewAPIClient(convertV2toV1Config(configv2))
	filter := "email:$in:" + strings.Join(unique, "|")
	users, err := listSystemUsers(client, filter, "_id", "_id")
	if err != nil {
		return nil, fmt.Errorf("error loading user IDs from emails: %s", err)
	}
	for _, u := range users {
		ids = append(ids, u.Id)
	}

	if len(ids) < len(unique) {
		// Name the missing ones (a second lookup, only on this error path).
		found := map[string]bool{}
		withEmails, err := listSystemUsers(client, filter, "email", "email")
		if err != nil {
			return nil, fmt.Errorf("error loading user IDs from emails: %s", err)
		}
		for _, u := range withEmails {
			found[strings.ToLower(u.Email)] = true
		}
		var missing []string
		for _, email := range unique {
			if !found[strings.ToLower(email)] {
				missing = append(missing, email)
			}
		}
		return nil, fmt.Errorf("no JumpCloud user found for member email(s): %s", strings.Join(missing, ", "))
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
