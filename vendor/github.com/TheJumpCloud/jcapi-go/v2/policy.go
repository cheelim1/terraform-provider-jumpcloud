/*
 * JumpCloud APIs
 *
 *  JumpCloud's V2 API. This set of endpoints allows JumpCloud customers to manage objects, groupings and mappings and interact with the JumpCloud Graph.
 *
 * API version: 2.0
 * Generated by: Swagger Codegen (https://github.com/swagger-api/swagger-codegen.git)
 */

package v2

// An instance of a policy template.
type Policy struct {

	// ObjectId uniquely identifying a Policy.
	Id string `json:"id,omitempty"`

	Template *PolicyTemplate `json:"template,omitempty"`

	// The description for this specific Policy.
	Name string `json:"name,omitempty"`
}