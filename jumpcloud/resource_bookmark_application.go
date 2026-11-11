package jumpcloud

import (
	"errors"
	"fmt"
	"log"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
)

// defaultBookmarkColor is the tile colour the JumpCloud console picks for a new
// bookmark application.
const defaultBookmarkColor = "#006CAC"

func resourceBookmarkApplication() *schema.Resource {
	return &schema.Resource{
		Description: "Provides a resource for a JumpCloud bookmark application: a tile in the " +
			"JumpCloud User Portal that links to a URL, with no single sign-on of its own. " +
			"Grant access to it with `jumpcloud_user_group_association` (type `application`).",
		Create: resourceBookmarkApplicationCreate,
		Read:   resourceBookmarkApplicationRead,
		Update: resourceBookmarkApplicationUpdate,
		// A bookmark is an ordinary application as far as the API is concerned.
		Delete: resourceApplicationDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Schema: map[string]*schema.Schema{
			"display_label": {
				Description: "Name of the bookmark, as shown to users in the User Portal.",
				Type:        schema.TypeString,
				Required:    true,
			},
			"url": {
				Description: "The URL the bookmark opens.",
				Type:        schema.TypeString,
				Required:    true,
			},
			"description": {
				Description: "Description of the bookmark.",
				Type:        schema.TypeString,
				Optional:    true,
			},
			"color": {
				Description: "Hex colour of the bookmark's tile, for example `#006CAC`. " +
					"Omit it to use JumpCloud's default colour.",
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"active": {
				Description: "Whether the bookmark is shown to the users it is assigned to.",
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     true,
			},
		},
	}
}

func resourceBookmarkApplicationCreate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))

	body := buildBookmarkApplicationRequestBody(nil, d)

	result, err := ApplicationCreateRaw(configv1.BasePath, configv1.DefaultHeader, body)
	if err != nil {
		return err
	}

	id, _ := result["_id"].(string)
	if id == "" {
		return fmt.Errorf("bookmark application created but no _id was returned")
	}
	log.Println("[INFO] id=", id)
	d.SetId(id)
	return resourceBookmarkApplicationRead(d, meta)
}

func resourceBookmarkApplicationRead(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))

	raw, err := ApplicationGetRaw(configv1.BasePath, configv1.DefaultHeader, d.Id())
	if err != nil {
		if errors.Is(err, ErrApplicationNotFound) {
			log.Printf("[WARN] bookmark application %s not found, removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return err
	}

	// Importing the ID of a SAML (or other) application would otherwise read
	// back as a bookmark with an empty URL, and the next apply would rewrite it.
	if !isBookmarkApplication(raw) {
		return fmt.Errorf("application %s is not a bookmark application; manage it with jumpcloud_application instead", d.Id())
	}

	// A successful GET of /applications/{id} is that application; don't let a
	// response that omits _id drop it from state.
	if v, _ := raw["_id"].(string); v != "" {
		d.SetId(v)
	}

	if v, ok := raw["displayLabel"].(string); ok {
		if err := d.Set("display_label", v); err != nil {
			return err
		}
	}
	if v, ok := raw["ssoUrl"].(string); ok {
		if err := d.Set("url", v); err != nil {
			return err
		}
	}
	if v, ok := raw["description"].(string); ok {
		if err := d.Set("description", v); err != nil {
			return err
		}
	}
	if v, ok := raw["active"].(bool); ok {
		if err := d.Set("active", v); err != nil {
			return err
		}
	}
	if v, ok := raw["color"].(string); ok {
		if err := d.Set("color", v); err != nil {
			return err
		}
	}
	return nil
}

func resourceBookmarkApplicationUpdate(d *schema.ResourceData, meta interface{}) error {
	configv1 := convertV2toV1Config(meta.(*jcapiv2.Configuration))

	// Read-modify-write, as for jumpcloud_application: start from the
	// application's current server-side state so that fields this resource does
	// not manage (logo, keywords, ...) are sent back unchanged.
	current, err := ApplicationGetRaw(configv1.BasePath, configv1.DefaultHeader, d.Id())
	if err != nil {
		return err
	}

	body := buildBookmarkApplicationRequestBody(current, d)
	if _, err := ApplicationUpdateRaw(configv1.BasePath, configv1.DefaultHeader, d.Id(), body); err != nil {
		return err
	}
	return resourceBookmarkApplicationRead(d, meta)
}

// isBookmarkApplication reports whether a raw application is a bookmark. The
// API stores that as sso.type "bookmark". `isBookmark` is only something the
// console sends on a write: confirmed against the live API, it is not returned
// by GET /applications/{id}.
func isBookmarkApplication(raw map[string]interface{}) bool {
	if sso, ok := raw["sso"].(map[string]interface{}); ok {
		if t, _ := sso["type"].(string); t == "bookmark" {
			return true
		}
	}
	isBookmark, _ := raw["isBookmark"].(bool)
	return isBookmark
}

// buildBookmarkApplicationRequestBody applies this resource's fields onto seed
// (nil for Create, or the application's current raw state for Update) and
// returns the result as the request body.
//
// On Create the body has the shape the JumpCloud console sends when an admin
// adds a bookmark (confirmed via a captured browser request): the application is
// the `bookmark` template with `isBookmark` set, an empty `config`, and the
// bookmark's URL in `ssoUrl`.
func buildBookmarkApplicationRequestBody(seed map[string]interface{}, d *schema.ResourceData) map[string]interface{} {
	body := seed
	isCreate := body == nil
	if isCreate {
		body = map[string]interface{}{
			"name":                      "bookmark",
			"displayName":               "Bookmark",
			"type":                      "application",
			"isBookmark":                true,
			"enabledCapabilities":       []interface{}{"bookmark"},
			"sso":                       map[string]interface{}{"type": "bookmark", "beta": false, "hidden": false, "jit": false, "spErrorFlow": false},
			"config":                    map[string]interface{}{},
			"databaseAttributes":        []interface{}{},
			"provision":                 nil,
			"beta":                      false,
			"id":                        "",
			"isCustomLogo":              false,
			"keywords":                  "",
			"learnMore":                 "",
			"scimPrimaryOrganizationId": "",
			"ssoDirName":                "",
			"ssoFileName":               "",
		}
	}

	// Not returned by GET, so absent from an Update's seed; the console always
	// sends it.
	body["isBookmark"] = true

	body["displayLabel"] = d.Get("display_label").(string)
	body["ssoUrl"] = d.Get("url").(string)
	body["description"] = d.Get("description").(string)
	body["active"] = d.Get("active").(bool)

	color := d.Get("color").(string)
	if color == "" && isCreate {
		color = defaultBookmarkColor
	}
	if color != "" {
		body["color"] = color
		logo, ok := body["logo"].(map[string]interface{})
		if !ok {
			logo = map[string]interface{}{}
			body["logo"] = logo
		}
		logo["color"] = color
	}

	return body
}
