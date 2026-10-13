package jumpcloud

// see https://www.terraform.io/docs/extend/writing-custom-providers.html#implementing-a-more-complex-read

import (
	"fmt"
	"strconv"
	"strings"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
)

func flattenAttributes(attr *jcapiv2.UserGroupAttributes) map[string]interface{} {
	return map[string]interface{}{
		"posix_groups": flattenPosixGroups(attr.PosixGroups),
		// "enable_samba": fmt.Sprintf("%t", attr.SambaEnabled),
	}
}

func flattenPosixGroups(pg []jcapiv2.UserGroupAttributesPosixGroups) string {
	out := []string{}
	for _, v := range pg {
		out = append(out, fmt.Sprintf("%d:%s", v.Id, v.Name))
	}
	return strings.Join(out, ",")
}

func expandAttributes(attr interface{}) (out *jcapiv2.UserGroupAttributes, ok bool) {
	if attr == nil {
		return
	}
	mapAttr, ok := attr.(map[string]interface{})
	if !ok {
		return
	}

	// var enableSamba bool
	// sambaStr, ok := mapAttr["enable_samba"].(string)
	// if ok {
	// 	enableSamba, _ = strconv.ParseBool(sambaStr)
	// }

	// TODO: empty string? nil?
	posixStr, ok := mapAttr["posix_groups"].(string)
	if !ok {
		return
	}

	groups := strings.Split(posixStr, ",")
	posixGroups := []jcapiv2.UserGroupAttributesPosixGroups{}
	for _, v := range groups {
		g := strings.Split(v, ":")
		if len(g) != 2 {
			return
		}
		id, err := strconv.ParseInt(g[0], 10, 32)
		if err != nil {
			continue
		}
		posixGroups = append(posixGroups,
			jcapiv2.UserGroupAttributesPosixGroups{
				Id: int32(id), Name: g[1],
			})
	}

	if len(posixGroups) == 0 {
		return
	}

	return &jcapiv2.UserGroupAttributes{
		PosixGroups: posixGroups,
		// SambaEnabled: enableSamba,
	}, true
}

// systemGroupAttributes are the keys of a user group's "attributes" object that
// JumpCloud itself defines. Every other key is a custom attribute.
var systemGroupAttributes = map[string]bool{
	"ldapGroups":   true,
	"posixGroups":  true,
	"sudo":         true,
	"radius":       true,
	"sambaEnabled": true,
}

// flattenCustomAttributes returns a group's string-valued custom attributes.
// Non-string ones (the console also allows number, boolean and JSON) can't be
// represented in a map of strings, so they're left out -- and, because
// applyCustomAttributes only touches keys it's told about, left alone.
func flattenCustomAttributes(attrs map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range attrs {
		if systemGroupAttributes[k] {
			continue
		}
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// applyCustomAttributes edits attrs (a group's raw "attributes" object) in
// place: keys in oldCustom but not newCustom are removed, and every key in
// newCustom is set. System attributes are never touched.
func applyCustomAttributes(attrs, oldCustom, newCustom map[string]interface{}) {
	for k := range oldCustom {
		if _, keep := newCustom[k]; !keep && !systemGroupAttributes[k] {
			delete(attrs, k)
		}
	}
	for k, v := range newCustom {
		if !systemGroupAttributes[k] {
			attrs[k] = v
		}
	}
}
