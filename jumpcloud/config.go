package jumpcloud

import (
	"net/http"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
)

const (
	headerAccept = "application/json"
)

// Config holds the JC configuration
type Config struct {
	APIKey string // User specific auth token
	OrgID  string // Organization ID
}

// Client instantiates a jcapiv2.Configuration struct that is passed
// to every Resource operation
func (c *Config) Client() (interface{}, error) {
	config := jcapiv2.NewConfiguration()
	// Set the HTTP client up front. jcapiv2.NewAPIClient fills in
	// http.DefaultClient on the configuration it's given when this is nil --
	// and every resource calls it on this one shared configuration, so with
	// Terraform's parallelism those writes race. Same client, set once.
	config.HTTPClient = http.DefaultClient
	config.AddDefaultHeader("x-api-key", c.APIKey)

	if c.OrgID != "" {
		config.AddDefaultHeader("x-org-id", c.OrgID)
	}
	// Instantiate the API client
	return config, nil
}
