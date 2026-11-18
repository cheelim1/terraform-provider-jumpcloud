package jumpcloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIdmsId       = "idms1"
	testDefaultRole  = "urn:ietf:params:scim:schemas:extension:2.0:User.defaultRole"
	testGroupAttrSrc = "jcGroups.attributes.snowflakeDefaultRole"
)

// fakeIdms is a stateful stand-in for the console's IDMS translation-rule
// endpoints, shaped after captured responses: a list of
// {"totalCount":N,"translationRules":[...]} and a bulk POST taking
// insert/update/delete lists. Rules start with two connector defaults, like a
// real provisioning config.
type fakeIdms struct {
	t        *testing.T
	mu       sync.Mutex // guards the fields below; the server handles requests concurrently
	rules    []map[string]interface{}
	nextId   int
	posts    []map[string]interface{}
	gets     int
	dropAdds bool // accept inserts but never list them
	// totalOverride, when non-zero, is reported as totalCount.
	totalOverride int
	// rawList, when set, is returned as the whole list response instead of the rules.
	rawList interface{}
	// postDelay is slept, outside the mutex, before a write is applied. It widens the window
	// between one client's insert request and the rule appearing in the list.
	postDelay time.Duration
}

func newFakeIdms(t *testing.T) *fakeIdms {
	return &fakeIdms{
		t:      t,
		nextId: 100,
		rules: []map[string]interface{}{
			{"id": "r1", "source": "email", "destination": "userName", "sourceType": "PATH",
				"default": true, "direction": "EXPORT", "appliedOn": []interface{}{"CREATE", "UPDATE"}},
			{"id": "r2", "source": "firstname", "destination": "name.givenName", "sourceType": "PATH",
				"default": true, "direction": "EXPORT", "appliedOn": []interface{}{"CREATE", "UPDATE"}},
		},
	}
}

func (f *fakeIdms) start() (*jcapiv2.Configuration, func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/idms/"+testIdmsId+"/translation-rules":
			f.mu.Lock()
			defer f.mu.Unlock()
			f.gets++
			if f.rawList != nil {
				_ = json.NewEncoder(w).Encode(f.rawList)
				return
			}
			total := len(f.rules)
			if f.totalOverride != 0 {
				total = f.totalOverride
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"totalCount": total, "translationRules": f.rules})
		case r.Method == http.MethodPost && r.URL.Path == "/idms/"+testIdmsId+"/bulk-translation-rules":
			var body map[string]interface{}
			require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
			time.Sleep(f.postDelay)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.posts = append(f.posts, body)
			for _, item := range body["insertTranslationRule"].([]interface{}) {
				if f.dropAdds {
					continue
				}
				rule := item.(map[string]interface{})
				f.nextId++
				f.rules = append(f.rules, map[string]interface{}{
					"id": fmt.Sprintf("n%d", f.nextId), "source": rule["source"], "destination": rule["destination"],
					"sourceType": rule["sourceType"], "direction": rule["direction"], "appliedOn": rule["appliedOn"],
					"default": false, "required": false, "editable": true, "resourceObjectId": "",
				})
			}
			for _, id := range body["deleteTranslationRuleObjectIds"].([]interface{}) {
				kept := f.rules[:0:0]
				for _, rule := range f.rules {
					if rule["id"] != id {
						kept = append(kept, rule)
					}
				}
				f.rules = kept
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			f.t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	config := jcapiv2.NewConfiguration()
	config.BasePath = server.URL
	config.AddDefaultHeader("x-api-key", "test-key")
	return config, server.Close
}

func scimMappingData(t *testing.T, raw map[string]interface{}) *schema.ResourceData {
	return schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, raw)
}

func defaultRoleMapping(t *testing.T) *schema.ResourceData {
	return scimMappingData(t, map[string]interface{}{
		"idms_id":     testIdmsId,
		"destination": testDefaultRole,
		"source":      testGroupAttrSrc,
		"source_type": "PATH",
	})
}

func TestScimAttributeMappingSchemaIsValid(t *testing.T) {
	require.NoError(t, resourceScimAttributeMapping().InternalValidate(nil, true))
}

// The SDK reports an empty set as unset, so an explicit `applied_on = []` would silently
// become the default of both operations. It must be rejected; omitting it is fine.
func TestScimAttributeMappingRejectsExplicitlyEmptyAppliedOn(t *testing.T) {
	base := map[string]interface{}{
		"idms_id":     testIdmsId,
		"destination": testDefaultRole,
		"source":      testGroupAttrSrc,
		"source_type": "PATH",
	}
	validate := func(extra map[string]interface{}) []error {
		raw := map[string]interface{}{}
		for k, v := range base {
			raw[k] = v
		}
		for k, v := range extra {
			raw[k] = v
		}
		_, errs := resourceScimAttributeMapping().Validate(terraform.NewResourceConfigRaw(raw))
		return errs
	}

	errs := validate(map[string]interface{}{"applied_on": []interface{}{}})
	require.NotEmpty(t, errs, "an explicitly empty applied_on must be rejected")
	assert.Contains(t, errs[0].Error(), "applied_on")

	assert.Empty(t, validate(nil), "omitting applied_on selects the default and is valid")
	assert.Empty(t, validate(map[string]interface{}{"applied_on": []interface{}{"CREATE"}}))
}

// Create is a list, check, insert, read-back sequence over one shared rule list, and
// Terraform runs independent resources in parallel. Two creates for the same destination
// must not both pass the duplicate check. postDelay holds each insert open long enough that
// without the per-IDMS lock the second create lists before the first rule appears.
func TestScimAttributeMappingConcurrentCreatesAreSerialized(t *testing.T) {
	fake := newFakeIdms(t)
	fake.postDelay = 150 * time.Millisecond
	config, closeServer := fake.start()
	defer closeServer()

	sources := []string{"valueA", "valueB"}
	errs := make([]error, len(sources))
	var wg sync.WaitGroup
	for i := range sources {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := scimMappingData(t, map[string]interface{}{
				"idms_id":     testIdmsId,
				"destination": "custom.attribute",
				"source":      sources[i],
				"source_type": "CONSTANT",
			})
			errs[i] = resourceScimAttributeMappingCreate(d, config)
		}(i)
	}
	wg.Wait()

	var succeeded, refused int
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case assert.Contains(t, err.Error(), "already has a mapping"):
			refused++
		}
	}
	assert.Equal(t, 1, succeeded, "exactly one create may succeed")
	assert.Equal(t, 1, refused, "the other must see the first rule and refuse")

	fake.mu.Lock()
	defer fake.mu.Unlock()
	var mapped int
	for _, rule := range fake.rules {
		if rule["destination"] == "custom.attribute" {
			mapped++
		}
	}
	assert.Equal(t, 1, mapped, "only one rule may exist for the destination")
}

// A response the provider can't vouch for must be an error, not an empty or short list:
// otherwise Read drops the rule from state and Delete reports success without deleting.
func TestScimAttributeMappingListRejectsMalformedResponses(t *testing.T) {
	cases := map[string]struct {
		raw  interface{}
		want string
	}{
		"missing totalCount":      {map[string]interface{}{"translationRules": []interface{}{}}, "totalCount"},
		"non-numeric totalCount":  {map[string]interface{}{"totalCount": "1", "translationRules": []interface{}{}}, "totalCount"},
		"negative totalCount":     {map[string]interface{}{"totalCount": -1, "translationRules": []interface{}{}}, "totalCount"},
		"fractional totalCount":   {map[string]interface{}{"totalCount": 1.5, "translationRules": []interface{}{}}, "totalCount"},
		"rules not a list":        {map[string]interface{}{"totalCount": 1, "translationRules": "x"}, "not a list"},
		"entry not an object":     {map[string]interface{}{"totalCount": 1, "translationRules": []interface{}{"x"}}, "not an object"},
		"count larger than rules": {map[string]interface{}{"totalCount": 2, "translationRules": []interface{}{map[string]interface{}{"id": "a"}}}, "incomplete"},
		"rules absent, count > 0": {map[string]interface{}{"totalCount": 3}, "incomplete"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeIdms(t)
			fake.rawList = tc.raw
			config, closeServer := fake.start()
			defer closeServer()

			_, err := idmsListTranslationRules(config.BasePath, config.DefaultHeader, testIdmsId)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)

			// Read keeps the resource in state, and Delete doesn't pretend it deleted.
			d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
			d.SetId(testIdmsId + "/a")
			require.Error(t, resourceScimAttributeMappingRead(d, config))
			assert.Equal(t, testIdmsId+"/a", d.Id(), "state must be kept")
			require.Error(t, resourceScimAttributeMappingDelete(d, config))
			assert.Empty(t, fake.posts, "no delete may be sent")
		})
	}
}

func TestScimAttributeMappingListAcceptsAnEmptyListWithZeroCount(t *testing.T) {
	for name, raw := range map[string]interface{}{
		"empty list":  map[string]interface{}{"totalCount": 0, "translationRules": []interface{}{}},
		"null list":   map[string]interface{}{"totalCount": 0, "translationRules": nil},
		"absent list": map[string]interface{}{"totalCount": 0},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeIdms(t)
			fake.rawList = raw
			config, closeServer := fake.start()
			defer closeServer()

			rules, err := idmsListTranslationRules(config.BasePath, config.DefaultHeader, testIdmsId)
			require.NoError(t, err)
			assert.Empty(t, rules)
		})
	}
}

// The body mirrors the captured console request: one inserted rule with
// appliedOn/destination/direction/source/sourceType/resourceType, the other
// two lists empty, and resourceType repeated at the top level.
func TestScimAttributeMappingCreateSendsCapturedBodyAndReadsBackId(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := defaultRoleMapping(t)
	require.NoError(t, resourceScimAttributeMappingCreate(d, config))

	require.Len(t, fake.posts, 1)
	assert.Equal(t, map[string]interface{}{
		"deleteTranslationRuleObjectIds": []interface{}{},
		"insertTranslationRule": []interface{}{map[string]interface{}{
			"appliedOn":    []interface{}{"CREATE", "UPDATE"},
			"destination":  testDefaultRole,
			"direction":    "EXPORT",
			"source":       testGroupAttrSrc,
			"sourceType":   "PATH",
			"resourceType": "USER",
		}},
		"updateTranslationRule": []interface{}{},
		"resourceType":          "USER",
	}, fake.posts[0])

	assert.Equal(t, testIdmsId+"/n101", d.Id())
	assert.Equal(t, "n101", d.Get("rule_id"))
	assert.Equal(t, testGroupAttrSrc, d.Get("source"))
}

func TestScimAttributeMappingCreateHonorsAppliedOn(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := scimMappingData(t, map[string]interface{}{
		"idms_id":     testIdmsId,
		"destination": "urn:ietf:params:scim:schemas:extension:2.0:User.defaultSecondaryRoles",
		"source":      "ALL",
		"source_type": "CONSTANT",
		"applied_on":  []interface{}{"UPDATE", "CREATE"},
	})
	require.NoError(t, resourceScimAttributeMappingCreate(d, config))

	insert := fake.posts[0]["insertTranslationRule"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, []interface{}{"CREATE", "UPDATE"}, insert["appliedOn"], "applied_on should be sent sorted so the body is deterministic")
	assert.Equal(t, "CONSTANT", insert["sourceType"])
}

// What happens with two rules for one destination hasn't been observed, so
// Create must refuse instead of adding a second one. Nothing is written.
func TestScimAttributeMappingCreateRefusesWhenDestinationAlreadyMapped(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := scimMappingData(t, map[string]interface{}{
		"idms_id":     testIdmsId,
		"destination": "userName",
		"source":      "employeeIdentifier",
		"source_type": "PATH",
	})
	err := resourceScimAttributeMappingCreate(d, config)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `already has a mapping for "userName"`)
	assert.Contains(t, err.Error(), "terraform import <address> idms1/r1")
	assert.Empty(t, fake.posts, "no write should be sent")
	assert.Empty(t, d.Id())
}

func TestScimAttributeMappingCreateErrorsWhenNewRuleCannotBeFound(t *testing.T) {
	fake := newFakeIdms(t)
	fake.dropAdds = true
	config, closeServer := fake.start()
	defer closeServer()

	d := defaultRoleMapping(t)
	err := resourceScimAttributeMappingCreate(d, config)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "0 new matching rules")
	assert.Empty(t, d.Id(), "an unresolvable rule id must not be guessed")
}

func TestScimAttributeMappingReadSetsFields(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()
	fake.rules = append(fake.rules, map[string]interface{}{
		"id": "x9", "source": testGroupAttrSrc, "destination": testDefaultRole, "sourceType": "PATH",
		"default": false, "direction": "EXPORT", "appliedOn": []interface{}{"UPDATE"},
	})

	d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
	d.SetId(testIdmsId + "/x9")
	require.NoError(t, resourceScimAttributeMappingRead(d, config))

	assert.Equal(t, testIdmsId, d.Get("idms_id"))
	assert.Equal(t, "x9", d.Get("rule_id"))
	assert.Equal(t, testDefaultRole, d.Get("destination"))
	assert.Equal(t, testGroupAttrSrc, d.Get("source"))
	assert.Equal(t, "PATH", d.Get("source_type"))
	assert.Equal(t, []string{"UPDATE"}, setToStrings(d.Get("applied_on").(*schema.Set)))
}

func TestScimAttributeMappingReadRemovesMissingRuleFromState(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
	d.SetId(testIdmsId + "/gone")
	require.NoError(t, resourceScimAttributeMappingRead(d, config))
	assert.Empty(t, d.Id())
}

func TestScimAttributeMappingReadRefusesConnectorDefaultRules(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
	d.SetId(testIdmsId + "/r1")
	err := resourceScimAttributeMappingRead(d, config)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "connector's own default")
}

// A truncated list would make an existing rule look deleted, so a short list
// must be an error, not a state removal.
func TestScimAttributeMappingReadFailsOnIncompleteList(t *testing.T) {
	fake := newFakeIdms(t)
	fake.totalOverride = 50
	config, closeServer := fake.start()
	defer closeServer()

	d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
	d.SetId(testIdmsId + "/n101")
	err := resourceScimAttributeMappingRead(d, config)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "incomplete")
	assert.Equal(t, testIdmsId+"/n101", d.Id(), "state must be kept")
}

func TestScimAttributeMappingRejectsMalformedId(t *testing.T) {
	for _, id := range []string{"noslash", "a/b/c", "/rule", "idms/"} {
		_, _, err := parseScimMappingId(id)
		assert.Error(t, err, id)
	}
	idms, rule, err := parseScimMappingId("idms1/abc")
	require.NoError(t, err)
	assert.Equal(t, "idms1", idms)
	assert.Equal(t, "abc", rule)
}

// The captured delete is a POST to the same bulk endpoint with the rule id in
// deleteTranslationRuleObjectIds and the other lists empty.
func TestScimAttributeMappingDeleteSendsCapturedBody(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := defaultRoleMapping(t)
	require.NoError(t, resourceScimAttributeMappingCreate(d, config))
	fake.posts = nil

	require.NoError(t, resourceScimAttributeMappingDelete(d, config))

	require.Len(t, fake.posts, 1)
	assert.Equal(t, map[string]interface{}{
		"deleteTranslationRuleObjectIds": []interface{}{"n101"},
		"insertTranslationRule":          []interface{}{},
		"updateTranslationRule":          []interface{}{},
		"resourceType":                   "USER",
	}, fake.posts[0])
	assert.Len(t, fake.rules, 2, "only the managed rule is removed")
}

func TestScimAttributeMappingDeleteSkipsRuleThatIsAlreadyGone(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
	d.SetId(testIdmsId + "/gone")
	require.NoError(t, resourceScimAttributeMappingDelete(d, config))
	assert.Empty(t, fake.posts, "an unknown id must not be sent to the API")
}

func TestScimAttributeMappingDeleteRefusesConnectorDefaultRules(t *testing.T) {
	fake := newFakeIdms(t)
	config, closeServer := fake.start()
	defer closeServer()

	d := schema.TestResourceDataRaw(t, resourceScimAttributeMapping().Schema, map[string]interface{}{})
	d.SetId(testIdmsId + "/r2")
	err := resourceScimAttributeMappingDelete(d, config)

	require.Error(t, err)
	assert.Empty(t, fake.posts)
}
