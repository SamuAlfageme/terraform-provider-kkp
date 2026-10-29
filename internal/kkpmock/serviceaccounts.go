package kkpmock

import (
	"fmt"
	"net/http"
	"sort"
)

type clusterSA struct {
	Obj map[string]any
	// tokenPolls is how many kubeconfig requests still find the token secret empty.
	tokenPolls int
}

// newBindings is what KKP's owner-binding-creator sets up in a new user cluster: one labelled binding per
// cluster role it manages, which the API then adds subjects to.
func newBindings() map[string]map[string]bool {
	return map[string]map[string]bool{"cluster-admin": {}, "edit": {}, "view": {}}
}

// ClusterServiceAccountRoles returns the cluster roles a cluster service account is bound to, sorted.
func (s *Server) ClusterServiceAccountRoles(projectID, clusterID, namespace, name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[projectID]
	if !ok {
		return nil
	}
	c, ok := p.clusters[clusterID]
	if !ok {
		return nil
	}
	var roles []string
	for role, subjects := range c.bindings {
		if subjects[namespace+"/"+name] {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return roles
}

func registerClusterSARoutes() {
	cid := `/api/v2/projects/([^/]+)/clusters/([^/]+)`
	clusterOf := func(s *Server, a args, sa string) (*cluster, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return nil, e
		}
		return s.cluster(p, a[1])
	}
	saOf := func(s *Server, a args, sa string) (*cluster, *clusterSA, *apiErr) {
		c, e := clusterOf(s, a, sa)
		if e != nil {
			return nil, nil, e
		}
		csa, ok := c.sas[a[2]+"/"+a[3]]
		if !ok {
			return nil, nil, fail(http.StatusNotFound, "serviceaccounts %q not found", a[3])
		}
		return c, csa, nil
	}

	handle("GET", cid+`/serviceaccount`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, list(c.sas, func(x *clusterSA) any { return x.Obj }), nil
	})
	handle("POST", cid+`/serviceaccount`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		name, ns := str(b, "name"), str(b, "namespace")
		if name == "" {
			return 0, nil, fail(http.StatusBadRequest, "name must be set")
		}
		if ns == "" {
			return 0, nil, fail(http.StatusBadRequest, "namespace must be set")
		}
		if _, ok := c.sas[ns+"/"+name]; ok {
			return 0, nil, fail(http.StatusConflict, "serviceaccounts %q already exists", name)
		}
		obj := map[string]any{"id": name, "name": name, "namespace": ns, "creationTimestamp": now()}
		c.sas[ns+"/"+name] = &clusterSA{Obj: obj, tokenPolls: 1}
		return 201, obj, nil
	})
	handle("DELETE", cid+`/serviceaccount/([^/]+)/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		c, _, e := saOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		for _, subjects := range c.bindings {
			delete(subjects, a[2]+"/"+a[3])
		}
		delete(c.sas, a[2]+"/"+a[3])
		return 200, nil, nil
	})
	handle("GET", cid+`/serviceaccount/([^/]+)/([^/]+)/kubeconfig`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		c, csa, e := saOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		if csa.tokenPolls > 0 {
			csa.tokenPolls--
			return 0, nil, fail(http.StatusInternalServerError, "no token defined in the service account's secret")
		}
		id := str(c.Obj, "id")
		return 200, rawBody(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: bW9jaw==
    server: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: sa-%s
  name: %s
current-context: %s
users:
- name: sa-%s
  user:
    token: mock-sa-token.%s.%s
`, str(c.Obj, "status", "url"), id, id, a[3], id, id, a[3], a[2], a[3])), nil
	})
	handle("GET", cid+`/serviceaccount/([^/]+)/([^/]+)/permissions`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		c, _, e := saOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		var out []any
		for role, subjects := range c.bindings {
			if subjects[a[2]+"/"+a[3]] {
				out = append(out, map[string]any{"scope": "Cluster", "roleRefName": role, "namespace": ""})
			}
		}
		return 200, out, nil
	})

	// Cluster role bindings: subjects are added to and removed from the binding KKP created for the role.
	binding := func(s *Server, a args, b body, sa string) (map[string]bool, string, *apiErr) {
		c, e := clusterOf(s, a, sa)
		if e != nil {
			return nil, "", e
		}
		name, ns := str(b, "serviceAccount"), str(b, "serviceAccountNamespace")
		if name == "" || ns == "" {
			return nil, "", fail(http.StatusBadRequest, "the mock only binds service accounts")
		}
		subjects, ok := c.bindings[a[2]]
		if !ok {
			return nil, "", fail(http.StatusInternalServerError, "the cluster role binding not found")
		}
		return subjects, ns + "/" + name, nil
	}
	handle("POST", cid+`/clusterroles/([^/]+)/clusterbindings`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		subjects, key, e := binding(s, a, b, sa)
		if e != nil {
			return 0, nil, e
		}
		if subjects[key] {
			return 0, nil, fail(http.StatusBadRequest, "service account %s already connected to the cluster role %s", key, a[2])
		}
		subjects[key] = true
		return 200, map[string]any{"roleRefName": a[2]}, nil
	})
	handle("DELETE", cid+`/clusterroles/([^/]+)/clusterbindings`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		subjects, key, e := binding(s, a, b, sa)
		if e != nil {
			return 0, nil, e
		}
		delete(subjects, key)
		return 200, map[string]any{"roleRefName": a[2]}, nil
	})
}
