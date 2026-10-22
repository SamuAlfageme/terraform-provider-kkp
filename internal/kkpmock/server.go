// Package kkpmock is an in-memory fake of the KKP REST API for the provider's acceptance tests.
//
// It copies the behaviour of the real API server that matters to a Terraform provider, including the
// quirks the dashboard rewrite ran into:
//   - empty lists are answered with null;
//   - projects start Inactive and turn Active a moment later;
//   - clusters take a few health polls to come up, machine deployments are refused with 503 until then;
//   - credentials are never returned: spec.cloud.openstack carries credentialsReference instead;
//   - PATCH is a JSON merge patch and only some cluster fields are taken from it, the rest are dropped;
//   - deletion is asynchronous (deletionTimestamp first, 404 a few polls later);
//   - service account and member groups come back suffixed with "-<project id>";
//   - service account tokens cannot create projects.
package kkpmock

import (
	"crypto/md5" //nolint:gosec // KKP shows MD5 fingerprints
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// AdminToken is accepted as a user (OIDC-like) token with access to every project.
const AdminToken = "admin-token"

// Server is a running fake KKP.
type Server struct {
	*httptest.Server

	mu sync.Mutex
	// HealthPolls is how many cluster health/get polls a new cluster stays provisioning for.
	HealthPolls int
	// DeletePolls is how many polls a deleted cluster or machine deployment stays visible for.
	DeletePolls int
	// FailNext makes the next n requests answer 502 (to exercise retries).
	FailNext int
	// LastDelete records the cleanup headers of the last cluster deletion.
	LastDelete LastClusterDelete

	projects map[string]*project
	// tokens maps a bearer token to the project of the service account it belongs to ("" = admin user).
	tokens   map[string]string
	requests []string
}

type project struct {
	Obj      map[string]any
	polls    int
	deleting int
	sshKeys  map[string]map[string]any
	sas      map[string]map[string]any
	saTokens map[string]map[string]map[string]any
	members  map[string]map[string]any
	clusters map[string]*cluster
}

type cluster struct {
	Obj      map[string]any
	sshKeys  map[string]bool
	polls    int
	deleting int
	mds      map[string]*md
}

type md struct {
	Obj      map[string]any
	deleting int
}

// Datacenters offered by the fake.
var Datacenters = []map[string]any{
	{"metadata": map[string]any{"name": "os-zurich"}, "spec": map[string]any{
		"seed": "europe", "provider": "openstack", "country": "CH", "location": "Zurich",
		"openstack": map[string]any{"authURL": "https://keystone.example.ch:5000/v3", "region": "zrh", "availabilityZone": "nova", "enforceFloatingIP": false, "images": map[string]any{"ubuntu": "Ubuntu 24.04", "flatcar": "Flatcar Stable"}},
	}},
	{"metadata": map[string]any{"name": "os-lausanne"}, "spec": map[string]any{
		"seed": "europe", "provider": "openstack", "country": "CH", "location": "Lausanne",
		"openstack": map[string]any{"authURL": "https://keystone.example.ch:5000/v3", "region": "lsn", "images": map[string]any{"ubuntu": "Ubuntu 24.04"}},
	}},
	{"metadata": map[string]any{"name": "aws-eu-central-1a"}, "spec": map[string]any{
		"seed": "europe", "provider": "aws", "country": "DE", "location": "Frankfurt",
		"aws": map[string]any{"region": "eu-central-1", "images": map[string]any{"ubuntu": "ami-0ubuntu2404", "flatcar": "ami-0flatcar", "amzn2": "ami-0amzn2"}},
	}},
	{"metadata": map[string]any{"name": "azure-westeurope"}, "spec": map[string]any{
		"seed": "europe", "provider": "azure", "country": "NL", "location": "Amsterdam",
		"azure": map[string]any{"location": "westeurope", "images": map[string]any{"ubuntu": "/subscriptions/0000/images/ubuntu-2404"}},
	}},
	{"metadata": map[string]any{"name": "gcp-europe-west3"}, "spec": map[string]any{
		"seed": "europe", "provider": "gcp", "country": "DE", "location": "Frankfurt",
		"gcp": map[string]any{"region": "europe-west3", "zoneSuffixes": []any{"a", "b", "c"}},
	}},
	{"metadata": map[string]any{"name": "hetzner-nbg1"}, "spec": map[string]any{
		"seed": "europe", "provider": "hetzner", "country": "DE", "location": "Nuremberg",
		"hetzner": map[string]any{"datacenter": "nbg1-dc3", "location": "nbg1", "network": "kubermatic"},
	}},
	{"metadata": map[string]any{"name": "do-ams3"}, "spec": map[string]any{
		"seed": "europe", "provider": "digitalocean", "country": "NL", "location": "Amsterdam",
		"digitalocean": map[string]any{"region": "ams3"},
	}},
}

// Versions offered for every provider.
var Versions = []string{"1.31.9", "1.32.5", "1.33.1"}

// Presets the fake knows (name → provider).
var Presets = map[string]string{"switch-openstack": "openstack", "team-aws": "aws", "team-azure": "azure", "team-gcp": "gcp", "team-hetzner": "hetzner", "team-do": "digitalocean"}

// New starts a fake KKP. Close it when done.
func New() *Server {
	s := &Server{HealthPolls: 2, DeletePolls: 1, projects: map[string]*project{}, tokens: map[string]string{AdminToken: ""}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Requests returns "METHOD path" for every request served so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// AddProject creates an Active project directly and returns its ID.
func (s *Server) AddProject(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.newProject(name, nil)
	p.polls = 1
	p.Obj["status"] = "Active"
	return str(p.Obj, "id")
}

// ServiceAccountToken mints a token for a new editor service account in projectID, like an admin would in the UI.
func (s *Server) ServiceAccountToken(projectID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[projectID]
	sa := s.addServiceAccount(p, "terraform", "editors")
	tok := s.addToken(p, str(sa, "id"), "terraform")
	return str(tok, "token")
}

func randID(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.IntN(len(letters))]
	}
	return string(b)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Server) newProject(name string, labels map[string]any) *project {
	id := randID(10)
	obj := map[string]any{"id": id, "name": name, "status": "Inactive", "creationTimestamp": now(),
		"owners": []any{map[string]any{"name": "Admin", "email": "admin@example.com"}}}
	if len(labels) > 0 {
		obj["labels"] = labels
	}
	p := &project{Obj: obj, sshKeys: map[string]map[string]any{}, sas: map[string]map[string]any{},
		saTokens: map[string]map[string]map[string]any{}, members: map[string]map[string]any{}, clusters: map[string]*cluster{}}
	p.members["admin"] = map[string]any{"id": "admin", "name": "Admin", "email": "admin@example.com", "creationTimestamp": now(),
		"projects": []any{map[string]any{"id": id, "group": "owners-" + id}}}
	s.projects[id] = p
	return p
}

func (s *Server) addServiceAccount(p *project, name, group string) map[string]any {
	pid := str(p.Obj, "id")
	id := "sa-" + randID(10)
	sa := map[string]any{"id": id, "name": name, "group": group + "-" + pid, "status": "Active", "creationTimestamp": now()}
	p.sas[id] = sa
	p.saTokens[id] = map[string]map[string]any{}
	return sa
}

func (s *Server) addToken(p *project, saID, name string) map[string]any {
	pid := str(p.Obj, "id")
	id := "sa-token-" + randID(10)
	claims, _ := json.Marshal(map[string]any{"email": saID + "@example.com", "project_id": pid, "token_id": id, "exp": time.Now().Add(3 * 365 * 24 * time.Hour).Unix()})
	enc := base64.RawURLEncoding.EncodeToString
	secret := enc([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc(claims) + "." + enc([]byte(randID(16)))
	t := map[string]any{"id": id, "name": name, "creationTimestamp": now(), "expiry": time.Now().Add(3 * 365 * 24 * time.Hour).UTC().Format(time.RFC3339)}
	p.saTokens[saID][id] = t
	s.tokens[secret] = pid
	out := clone(t)
	out["token"] = secret
	return out
}

// HTTP plumbing

type apiErr struct {
	code int
	msg  string
}

func fail(code int, format string, args ...any) *apiErr {
	return &apiErr{code: code, msg: fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// list answers like KKP: null for an empty list, sorted by name otherwise.
func list[T any](m map[string]T, conv func(T) any) any {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(m))
	for _, k := range keys {
		out = append(out, conv(m[k]))
	}
	return out
}

type route struct {
	method  string
	pattern *regexp.Regexp
	handle  func(s *Server, r *http.Request, args []string, body map[string]any, saProject string) (int, any, *apiErr)
}

var routes []route

func handle(method, pattern string, h func(s *Server, r *http.Request, args []string, body map[string]any, saProject string) (int, any, *apiErr)) {
	routes = append(routes, route{method, regexp.MustCompile("^" + pattern + "$"), h})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)

	if s.FailNext > 0 {
		s.FailNext--
		http.Error(w, "upstream connect error", http.StatusBadGateway)
		return
	}

	auth := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(auth, "Bearer ")
	saProject, known := s.tokens[token]
	if !ok || !known {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "message": "invalid token"}})
		return
	}

	var body map[string]any
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": 400, "message": "cannot decode body: " + err.Error()}})
				return
			}
		}
	}

	for _, rt := range routes {
		if rt.method != r.Method {
			continue
		}
		m := rt.pattern.FindStringSubmatch(r.URL.Path)
		if m == nil {
			continue
		}
		code, out, e := rt.handle(s, r, m[1:], body, saProject)
		if e != nil {
			writeJSON(w, e.code, map[string]any{"error": map[string]any{"code": e.code, "message": e.msg}})
			return
		}
		if raw, ok := out.(rawBody); ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(code)
			_, _ = w.Write(raw)
			return
		}
		writeJSON(w, code, out)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": 404, "message": "no route " + r.Method + " " + r.URL.Path}})
}

type rawBody []byte

// project lookup with service account scoping. Deleted projects disappear once their deletion polls are used up.
func (s *Server) project(id, saProject string) (*project, *apiErr) {
	p, ok := s.projects[id]
	if ok && p.deleting > 0 {
		p.deleting--
		if p.deleting == 0 {
			delete(s.projects, id)
			ok = false
		}
	}
	if !ok {
		return nil, fail(http.StatusNotFound, "projects.kubermatic.k8c.io %q not found", id)
	}
	if saProject != "" && saProject != id {
		return nil, fail(http.StatusForbidden, "forbidden: the service account is not a member of project %s", id)
	}
	return p, nil
}

func (s *Server) cluster(p *project, id string) (*cluster, *apiErr) {
	c, ok := p.clusters[id]
	if ok && c.deleting > 0 {
		c.deleting--
		if c.deleting == 0 {
			delete(p.clusters, id)
			ok = false
		}
	}
	if !ok {
		return nil, fail(http.StatusNotFound, "clusters.kubermatic.k8c.io %q not found", id)
	}
	return c, nil
}

func clone(m map[string]any) map[string]any {
	raw, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// mergePatch applies an RFC 7386 JSON merge patch.
func mergePatch(target, patch map[string]any) map[string]any {
	if target == nil {
		target = map[string]any{}
	}
	for k, v := range patch {
		if v == nil {
			delete(target, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			tm, _ := target[k].(map[string]any)
			target[k] = mergePatch(tm, pm)
			continue
		}
		target[k] = v
	}
	return target
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	s, _ := cur.(string)
	return s
}

func obj(m map[string]any, path ...string) map[string]any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	mm, _ := cur.(map[string]any)
	return mm
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func versionLess(a, b string) bool {
	var a1, a2, a3, b1, b2, b3 int
	_, _ = fmt.Sscanf(a, "%d.%d.%d", &a1, &a2, &a3)
	_, _ = fmt.Sscanf(b, "%d.%d.%d", &b1, &b2, &b3)
	if a1 != b1 {
		return a1 < b1
	}
	if a2 != b2 {
		return a2 < b2
	}
	return a3 < b3
}

func minor(v string) int {
	var a, b int
	_, _ = fmt.Sscanf(v, "%d.%d", &a, &b)
	return b
}

func fingerprint(pub string) (string, bool) {
	fields := strings.Fields(pub)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") && !strings.HasPrefix(fields[0], "ecdsa-") {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", false
	}
	sum := md5.Sum(raw) //nolint:gosec
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":"), true
}
