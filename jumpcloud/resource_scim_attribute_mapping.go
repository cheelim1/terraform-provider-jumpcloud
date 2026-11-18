package jumpcloud

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
)

// The only rule direction and resource type observed so far. EXPORT pushes
// from JumpCloud to the downstream application (SCIM); USER rules map a
// JumpCloud user to a downstream user attribute.
const (
	scimRuleDirection    = "EXPORT"
	scimRuleResourceType = "USER"
)

// idmsLocks serializes Create per provisioning config (IDMS id) within this provider
// process. Terraform runs independent resources in parallel, and Create is a
// list, check, insert, read-back sequence over one shared rule list: without this, two
// mappings for the same destination could both pass the duplicate check, or each could
// mistake the other's new rule for its own. It cannot serialize separate Terraform runs.
var idmsLocks sync.Map // idms id -> *sync.Mutex

func lockIdms(idmsId string) (unlock func()) {
	m, _ := idmsLocks.LoadOrStore(idmsId, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// resourceScimAttributeMapping manages one attribute mapping ("translation
// rule") on the Provisioning tab of a JumpCloud application that provisions
// over SCIM. It is built from captured browser requests against the console's
// IDMS API, not from documentation:
//
//	GET  /idms/{idms_id}/translation-rules       -> {"totalCount":N,"translationRules":[{id,...}]}
//	POST /idms/{idms_id}/bulk-translation-rules  -> {"insertTranslationRule":[...],
//	     "updateTranslationRule":[...], "deleteTranslationRuleObjectIds":[...],
//	     "resourceType":"USER"}
//
// Only insert and delete were captured, so there is no Update: every field is
// ForceNew and a change replaces the rule. A rule's id is not in any captured
// write response, so Create re-lists the rules and picks out the new one.
func resourceScimAttributeMapping() *schema.Resource {
	return &schema.Resource{
		Description: "Manages one attribute mapping on the Provisioning (SCIM) tab of a " +
			"JumpCloud application, e.g. mapping a group's custom attribute to a " +
			"downstream user attribute. Built from captured browser requests against " +
			"the console's IDMS API -- not documented anywhere else.\n\n" +
			"Only the `USER` resource type is supported. Rules the connector ships " +
			"with (`default: true` in the API) can't be managed here. Changing any " +
			"argument replaces the rule, because no update call has been captured. " +
			"Mapping changes may need a resync from the console before they reach " +
			"the downstream application; the console's resync endpoint is " +
			"rate-limited and is not called by this resource.",
		Create: resourceScimAttributeMappingCreate,
		Read:   resourceScimAttributeMappingRead,
		Delete: resourceScimAttributeMappingDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"idms_id": {
				Description: "The application's provisioning (IDMS) id: the id in the " +
					"`/api/v2/idms/<id>/...` requests the console makes from the " +
					"application's Provisioning tab. It is not the application id, and no " +
					"way to derive it from the application id has been found.",
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"destination": {
				Description: "The downstream (SCIM) attribute to set, e.g. " +
					"`urn:ietf:params:scim:schemas:extension:2.0:User.defaultRole`.",
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"source": {
				Description: "Where the value comes from. For `PATH`, a JumpCloud attribute " +
					"path such as `email` or `jcGroups.attributes.myAttribute` (a custom " +
					"attribute of the user's groups); for `CONSTANT`, the literal value; " +
					"for `EXPR`, an expression in JumpCloud's mapping language.",
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"source_type": {
				Description:  "`PATH`, `CONSTANT` or `EXPR`.",
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice([]string{"PATH", "CONSTANT", "EXPR"}, false),
			},
			"applied_on": {
				Description: "When the rule fires: any of `CREATE` and `UPDATE`. Defaults to " +
					"both, which is what the console sets. Omit it for the default: an " +
					"empty set is rejected, not treated as \"none\".",
				Type:     schema.TypeSet,
				Optional: true,
				Computed: true,
				ForceNew: true,
				// The SDK reports an empty set as unset, so an explicit `applied_on = []`
				// would silently become the default of both. Reject it instead.
				MinItems: 1,
				Elem: &schema.Schema{
					Type:         schema.TypeString,
					ValidateFunc: validation.StringInSlice([]string{"CREATE", "UPDATE"}, false),
				},
			},
			"rule_id": {
				Description: "The rule's own id, as used in the delete call.",
				Type:        schema.TypeString,
				Computed:    true,
			},
		},
	}
}

// parseScimMappingId splits the resource id "<idms_id>/<rule_id>".
func parseScimMappingId(id string) (idmsId, ruleId string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("unexpected id %q: expected <idms_id>/<rule_id>", id)
	}
	return parts[0], parts[1], nil
}

// idmsListTranslationRules lists a provisioning config's translation rules. It
// fails rather than return a list it can't vouch for: a rule missing from a
// truncated or malformed list would be mistaken for a deleted one (Read would drop it
// from state, Delete would report success without deleting). So the response must
// carry a numeric totalCount that equals the number of rule objects returned. An absent
// or null rule list is accepted only with a totalCount of 0.
func idmsListTranslationRules(basePath string, headers map[string]string, idmsId string) ([]map[string]interface{}, error) {
	raw, err := jcGetRaw(basePath, headers, "/idms/"+idmsId+"/translation-rules")
	if err != nil {
		return nil, err
	}

	total, ok := raw["totalCount"].(float64)
	if !ok || total != math.Trunc(total) || total < 0 {
		return nil, fmt.Errorf("translation rules list for %s has no valid numeric totalCount; unexpected response shape", idmsId)
	}

	var rules []map[string]interface{}
	if rawRules := raw["translationRules"]; rawRules != nil {
		list, ok := rawRules.([]interface{})
		if !ok {
			return nil, fmt.Errorf("translation rules list for %s: translationRules is not a list; unexpected response shape", idmsId)
		}
		for i, item := range list {
			rule, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("translation rules list for %s: entry %d is not an object; unexpected response shape", idmsId, i)
			}
			rules = append(rules, rule)
		}
	}
	if int(total) != len(rules) {
		return nil, fmt.Errorf("translation rules list for %s is incomplete (%d of %d returned); pagination isn't supported", idmsId, len(rules), int(total))
	}
	return rules, nil
}

// idmsWriteTranslationRules sends one bulk diff of rules to a provisioning config.
func idmsWriteTranslationRules(basePath string, headers map[string]string, idmsId string, insert []interface{}, deleteIds []string) error {
	if insert == nil {
		insert = []interface{}{}
	}
	if deleteIds == nil {
		deleteIds = []string{}
	}
	body := map[string]interface{}{
		"deleteTranslationRuleObjectIds": deleteIds,
		"insertTranslationRule":          insert,
		"updateTranslationRule":          []interface{}{},
		"resourceType":                   scimRuleResourceType,
	}
	_, err := jcWriteRaw(http.MethodPost, basePath, headers, "/idms/"+idmsId+"/bulk-translation-rules", body)
	return err
}

func scimRuleString(rule map[string]interface{}, key string) string {
	return stringOr(rule[key], "")
}

func findScimRuleById(rules []map[string]interface{}, ruleId string) map[string]interface{} {
	for _, rule := range rules {
		if scimRuleString(rule, "id") == ruleId {
			return rule
		}
	}
	return nil
}

func resourceScimAttributeMappingCreate(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	idmsId := d.Get("idms_id").(string)
	destination := d.Get("destination").(string)
	source := d.Get("source").(string)
	sourceType := d.Get("source_type").(string)

	appliedOn := []string{"CREATE", "UPDATE"}
	if v, ok := d.GetOk("applied_on"); ok {
		appliedOn = setToStrings(v.(*schema.Set))
	}

	// Hold the lock across the whole list, check, insert and read-back sequence.
	unlock := lockIdms(idmsId)
	defer unlock()

	before, err := idmsListTranslationRules(config.BasePath, headers, idmsId)
	if err != nil {
		return err
	}
	// What happens with two rules for one destination hasn't been observed, so
	// refuse to create one rather than guess which would win.
	existing := map[string]bool{}
	for _, rule := range before {
		existing[scimRuleString(rule, "id")] = true
		if scimRuleString(rule, "destination") == destination {
			return fmt.Errorf("%s already has a mapping for %q (rule %s, source %q); remove it first, or import it with `terraform import <address> %s/%s`",
				idmsId, destination, scimRuleString(rule, "id"), scimRuleString(rule, "source"), idmsId, scimRuleString(rule, "id"))
		}
	}

	insert := []interface{}{map[string]interface{}{
		"appliedOn":    appliedOn,
		"destination":  destination,
		"direction":    scimRuleDirection,
		"source":       source,
		"sourceType":   sourceType,
		"resourceType": scimRuleResourceType,
	}}
	if err := idmsWriteTranslationRules(config.BasePath, headers, idmsId, insert, nil); err != nil {
		return err
	}

	// No captured write response carries the new rule's id, so find it as the
	// one new, non-default rule that matches what was just sent.
	after, err := idmsListTranslationRules(config.BasePath, headers, idmsId)
	if err != nil {
		return fmt.Errorf("the mapping was created but its id couldn't be read back: %w", err)
	}
	var created []string
	for _, rule := range after {
		id := scimRuleString(rule, "id")
		if existing[id] || boolOr(rule["default"], false) {
			continue
		}
		if scimRuleString(rule, "destination") == destination &&
			scimRuleString(rule, "source") == source &&
			scimRuleString(rule, "sourceType") == sourceType &&
			scimRuleString(rule, "direction") == scimRuleDirection {
			created = append(created, id)
		}
	}
	if len(created) != 1 {
		return fmt.Errorf("the mapping was sent, but %d new matching rules were found in %s instead of one; check the Provisioning tab and import or remove the result", len(created), idmsId)
	}

	d.SetId(idmsId + "/" + created[0])
	return resourceScimAttributeMappingRead(d, meta)
}

func resourceScimAttributeMappingRead(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	idmsId, ruleId, err := parseScimMappingId(d.Id())
	if err != nil {
		return err
	}

	rules, err := idmsListTranslationRules(config.BasePath, headers, idmsId)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d.SetId("")
			return nil
		}
		return err
	}
	rule := findScimRuleById(rules, ruleId)
	if rule == nil {
		d.SetId("")
		return nil
	}
	if boolOr(rule["default"], false) {
		return fmt.Errorf("rule %s is one of the connector's own default mappings; those can't be managed with this resource", ruleId)
	}

	for k, v := range map[string]interface{}{
		"idms_id":     idmsId,
		"rule_id":     ruleId,
		"destination": scimRuleString(rule, "destination"),
		"source":      scimRuleString(rule, "source"),
		"source_type": scimRuleString(rule, "sourceType"),
		"applied_on":  stringSliceFromAny(rule["appliedOn"]),
	} {
		if err := d.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}

func resourceScimAttributeMappingDelete(d *schema.ResourceData, meta interface{}) error {
	config := meta.(*jcapiv2.Configuration)
	headers := config.DefaultHeader

	idmsId, ruleId, err := parseScimMappingId(d.Id())
	if err != nil {
		return err
	}

	// Don't send a delete for a rule that's already gone; what the API does
	// with an unknown id hasn't been observed.
	rules, err := idmsListTranslationRules(config.BasePath, headers, idmsId)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	rule := findScimRuleById(rules, ruleId)
	if rule == nil {
		return nil
	}
	if boolOr(rule["default"], false) {
		return fmt.Errorf("rule %s is one of the connector's own default mappings; refusing to delete it", ruleId)
	}

	return idmsWriteTranslationRules(config.BasePath, headers, idmsId, nil, []string{ruleId})
}

func setToStrings(s *schema.Set) []string {
	out := make([]string, 0, s.Len())
	for _, v := range s.List() {
		out = append(out, v.(string))
	}
	sort.Strings(out)
	return out
}
