package jumpcloud

// A stateful fake of the JumpCloud v1 system user API and the v2 group/graph
// API, for hermetic tests of jumpcloud_user, jumpcloud_user_group_membership,
// jumpcloud_user_group_association, jumpcloud_system_group and the user /
// user group data sources. v1 paths are served at the root, v2 under /v2 --
// matching directoryTestProviders, where the provider's v2 base path is
// url+"/v2" and convertV2toV1Config derives the v1 base path (url).
//
// Modelled API behaviour:
//   - PUT /systemusers/{id} is a partial update: keys the request omits keep
//     their value (the master provider has always relied on this).
//   - Lists default to 10 results when limit is absent or 0, as JumpCloud's do,
//     and cap at 100.
//   - Missing objects answer 404.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

type fakeEdge struct{ id, typ string }

type fakeDirectory struct {
	mu           sync.Mutex
	users        map[string]map[string]interface{}
	userPuts     []map[string]interface{} // every PUT /systemusers/{id} body
	members      map[string][]string      // group id -> user ids
	assocs       map[string][]fakeEdge    // group id -> associated objects
	userGroups   []map[string]interface{} // for GET /v2/usergroups
	systemGroups map[string]map[string]interface{}
	nextID       int

	// deletedGroups answer 404 on their members/associations lists, as a
	// group deleted in the console would.
	deletedGroups map[string]bool
	// hideNewEdgeReads, when > 0, keeps a just-added member or association
	// out of the next N list responses (eventual consistency).
	hideNewEdgeReads int
	hidden           map[string]int // "group/id" -> remaining hidden reads
	deletes          int            // DELETE requests and graph "remove" ops served
}

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{
		users:         map[string]map[string]interface{}{},
		members:       map[string][]string{},
		assocs:        map[string][]fakeEdge{},
		systemGroups:  map[string]map[string]interface{}{},
		deletedGroups: map[string]bool{},
		hidden:        map[string]int{},
	}
}

// visible reports whether edge key should be listed now, counting down any
// remaining hidden reads.
func (f *fakeDirectory) visible(key string) bool {
	if n := f.hidden[key]; n > 0 {
		f.hidden[key] = n - 1
		return false
	}
	return true
}

func (f *fakeDirectory) newID(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s%d", prefix, f.nextID)
}

func pageParams(r *http.Request) (limit, skip int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	skip, _ = strconv.Atoi(r.URL.Query().Get("skip"))
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	return
}

func page[T any](items []T, limit, skip int) []T {
	if skip >= len(items) {
		return []T{}
	}
	end := skip + limit
	if end > len(items) {
		end = len(items)
	}
	return items[skip:end]
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"Not Found"}`))
}

func (f *fakeDirectory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	enc := json.NewEncoder(w)
	p := r.URL.Path
	seg := strings.Split(strings.Trim(p, "/"), "/")

	switch {
	// ---- v1 system users
	case p == "/systemusers" && r.Method == http.MethodPost:
		id := f.newID("u")
		body["_id"] = id
		f.users[id] = body
		_ = enc.Encode(body)
	case p == "/systemusers" && r.Method == http.MethodGet:
		f.listUsers(w, r)
	case len(seg) == 2 && seg[0] == "systemusers":
		u, ok := f.users[seg[1]]
		if !ok {
			if r.Method == http.MethodDelete {
				f.deletes++
			}
			notFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = enc.Encode(u)
		case http.MethodPut:
			f.userPuts = append(f.userPuts, deepCopyJSON(body))
			for k, v := range body {
				u[k] = v
			}
			_ = enc.Encode(u)
		case http.MethodDelete:
			f.deletes++
			delete(f.users, seg[1])
			_ = enc.Encode(u)
		}
	case p == "/search/systemusers" && r.Method == http.MethodPost:
		var results []interface{}
		filters, _ := body["filter"].([]interface{})
		for _, u := range f.sortedUsers("_id") {
			for _, flt := range filters {
				m, _ := flt.(map[string]interface{})
				if m["email"] != nil && m["email"] == u["email"] {
					results = append(results, u)
				}
			}
		}
		if filters == nil { // no filter: everything, like the real search
			for _, u := range f.sortedUsers("_id") {
				results = append(results, u)
			}
		}
		_ = enc.Encode(map[string]interface{}{"totalCount": len(results), "results": results})

	// ---- v2 user groups
	case p == "/v2/usergroups" && r.Method == http.MethodPost:
		g := deepCopyJSON(body)
		g["id"] = f.newID("ug")
		g["type"] = "user_group"
		f.userGroups = append(f.userGroups, g)
		_ = enc.Encode(g)
	case len(seg) == 3 && seg[0] == "v2" && seg[1] == "usergroups":
		idx := -1
		for i, g := range f.userGroups {
			if g["id"] == seg[2] {
				idx = i
			}
		}
		if idx < 0 {
			notFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = enc.Encode(f.userGroups[idx])
		case http.MethodPatch: // behaves like PUT, as the real endpoint does
			g := deepCopyJSON(body)
			g["id"], g["type"] = seg[2], "user_group"
			f.userGroups[idx] = g
			_ = enc.Encode(g)
		case http.MethodDelete:
			f.deletes++
			f.userGroups = append(f.userGroups[:idx], f.userGroups[idx+1:]...)
			w.WriteHeader(http.StatusNoContent)
		}
	case p == "/v2/usergroups" && r.Method == http.MethodGet:
		groups := f.userGroups
		for _, flt := range r.URL.Query()["filter"] {
			if name := strings.TrimPrefix(flt, "name:eq:"); name != flt {
				var matched []map[string]interface{}
				for _, g := range groups {
					if g["name"] == name {
						matched = append(matched, g)
					}
				}
				groups = matched
			}
		}
		limit, skip := pageParams(r)
		_ = enc.Encode(page(groups, limit, skip))
	case len(seg) == 4 && seg[0] == "v2" && seg[1] == "usergroups" && seg[3] == "members":
		gid := seg[2]
		if f.deletedGroups[gid] {
			notFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			limit, skip := pageParams(r)
			var listed []string
			for _, uid := range f.members[gid] {
				if f.visible(gid + "/" + uid) {
					listed = append(listed, uid)
				}
			}
			out := []interface{}{}
			for _, uid := range page(listed, limit, skip) {
				out = append(out, map[string]interface{}{"to": map[string]interface{}{"id": uid, "type": "user"}})
			}
			_ = enc.Encode(out)
		case http.MethodPost:
			uid, _ := body["id"].(string)
			if body["op"] == "add" {
				if _, ok := f.users[uid]; !ok && !strings.HasPrefix(uid, "bulk") {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"message":"invalid user id"}`))
					return
				}
				f.members[gid] = append(f.members[gid], uid)
				if f.hideNewEdgeReads > 0 {
					f.hidden[gid+"/"+uid] = f.hideNewEdgeReads
				}
			} else {
				f.deletes++
				idx := indexOf(f.members[gid], uid)
				if idx < 0 {
					notFound(w)
					return
				}
				f.members[gid] = append(f.members[gid][:idx], f.members[gid][idx+1:]...)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	case len(seg) == 4 && seg[0] == "v2" && seg[1] == "usergroups" && seg[3] == "associations":
		gid := seg[2]
		if f.deletedGroups[gid] {
			notFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			targets := strings.Split(r.URL.Query().Get("targets"), ",")
			var matched []fakeEdge
			for _, e := range f.assocs[gid] {
				if stringInSlice(e.typ, targets) && f.visible(gid+"/"+e.id) {
					matched = append(matched, e)
				}
			}
			limit, skip := pageParams(r)
			out := []interface{}{}
			for _, e := range page(matched, limit, skip) {
				out = append(out, map[string]interface{}{"to": map[string]interface{}{"id": e.id, "type": e.typ}})
			}
			_ = enc.Encode(out)
		case http.MethodPost:
			e := fakeEdge{id: body["id"].(string), typ: body["type"].(string)}
			if body["op"] == "add" {
				f.assocs[gid] = append(f.assocs[gid], e)
				if f.hideNewEdgeReads > 0 {
					f.hidden[gid+"/"+e.id] = f.hideNewEdgeReads
				}
			} else {
				f.deletes++
				found := false
				for i, x := range f.assocs[gid] {
					if x == e {
						f.assocs[gid] = append(f.assocs[gid][:i], f.assocs[gid][i+1:]...)
						found = true
						break
					}
				}
				if !found {
					notFound(w)
					return
				}
			}
			w.WriteHeader(http.StatusNoContent)
		}

	// ---- v2 system groups
	case p == "/v2/systemgroups" && r.Method == http.MethodPost:
		id := f.newID("sg")
		g := map[string]interface{}{"id": id, "name": body["name"], "type": "system_group"}
		f.systemGroups[id] = g
		_ = enc.Encode(g)
	case p == "/v2/systemgroups" && r.Method == http.MethodGet:
		out := []interface{}{}
		for _, g := range f.systemGroups {
			for _, flt := range r.URL.Query()["filter"] {
				if g["name"] == strings.TrimPrefix(flt, "name:eq:") {
					out = append(out, g)
				}
			}
		}
		_ = enc.Encode(out)
	case len(seg) == 3 && seg[0] == "v2" && seg[1] == "systemgroups":
		g, ok := f.systemGroups[seg[2]]
		if !ok {
			if r.Method == http.MethodDelete {
				f.deletes++
			}
			notFound(w)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = enc.Encode(g)
		case http.MethodPut:
			g["name"] = body["name"]
			_ = enc.Encode(g)
		case http.MethodDelete:
			f.deletes++
			delete(f.systemGroups, seg[2])
			w.WriteHeader(http.StatusNoContent)
		}
	default:
		notFound(w)
	}
}

func indexOf(list []string, v string) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}

func (f *fakeDirectory) sortedUsers(key string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i][key]) < fmt.Sprint(out[j][key]) })
	return out
}

// listUsers serves GET /systemusers with the "<field>:$in:a|b" filter the
// provider uses to map member ids <-> emails.
func (f *fakeDirectory) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sortKey := q.Get("sort")
	if sortKey == "" {
		sortKey = "_id"
	}
	var matched []map[string]interface{}
	field, values := "", []string(nil)
	if flt := q.Get("filter"); flt != "" {
		parts := strings.SplitN(flt, ":$in:", 2)
		field, values = parts[0], strings.Split(parts[1], "|")
	}
	for _, u := range f.sortedUsers(sortKey) {
		if field == "" || stringInSlice(fmt.Sprint(u[field]), values) {
			matched = append(matched, u)
		}
	}
	limit, skip := pageParams(r)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"totalCount": len(matched), "results": page(matched, limit, skip)})
}

func (f *fakeDirectory) user(id string) map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return deepCopyJSON(f.users[id])
}

func (f *fakeDirectory) onlyUserID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.users {
		return id
	}
	return ""
}

func (f *fakeDirectory) do(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// directoryTestProviders points the provider at url: v2 calls at url+"/v2",
// v1 calls (via convertV2toV1Config) at url.
func directoryTestProviders(url string) map[string]terraform.ResourceProvider {
	p := Provider()
	p.ConfigureFunc = func(d *schema.ResourceData) (interface{}, error) {
		return directoryTestMeta(url), nil
	}
	return map[string]terraform.ResourceProvider{"jumpcloud": p}
}

func directoryTestMeta(url string) *jcapiv2.Configuration {
	c := jcapiv2.NewConfiguration()
	c.HTTPClient = http.DefaultClient // as Config.Client does; see there
	c.BasePath = url + "/v2"
	c.AddDefaultHeader("x-api-key", "k")
	return c
}

func newDirectoryServer(t *testing.T) (*fakeDirectory, *httptest.Server) {
	t.Helper()
	t.Setenv("JUMPCLOUD_API_KEY", "k")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	f := newFakeDirectory()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}
