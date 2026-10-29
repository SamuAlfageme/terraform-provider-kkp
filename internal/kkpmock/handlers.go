package kkpmock

import (
	"net/http"
	"slices"
	"strings"
)

type args = []string
type body = map[string]any

const (
	pid = `/api/v1/projects/([^/]+)`
	cid = `/api/v2/projects/([^/]+)/clusters/([^/]+)`
)

func init() {
	handle("GET", `/api/v1/version`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		return 200, map[string]any{"api": "v2.31.0-mock"}, nil
	})

	// Projects
	handle("GET", `/api/v1/projects`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		visible := map[string]*project{}
		for id, p := range s.projects {
			if sa == "" || sa == id {
				visible[id] = p
			}
		}
		return 200, list(visible, func(p *project) any { s.tick(p); return p.Obj }), nil
	})
	handle("POST", `/api/v1/projects`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		if sa != "" {
			return 0, nil, fail(http.StatusForbidden, "the Service Account is not allowed to create a project")
		}
		name := str(b, "name")
		if name == "" {
			return 0, nil, fail(http.StatusBadRequest, "the name of the project cannot be empty")
		}
		labels, _ := b["labels"].(map[string]any)
		p := s.newProject(name, labels)
		return 201, p.Obj, nil
	})
	handle("GET", pid, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		s.tick(p)
		return 200, p.Obj, nil
	})
	handle("PUT", pid, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if str(b, "name") == "" {
			return 0, nil, fail(http.StatusBadRequest, "the name of the project cannot be empty")
		}
		p.Obj["name"] = b["name"]
		if l, ok := b["labels"].(map[string]any); ok && len(l) > 0 {
			p.Obj["labels"] = l
		} else {
			delete(p.Obj, "labels")
		}
		return 200, p.Obj, nil
	})
	handle("DELETE", pid, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if sa != "" {
			return 0, nil, fail(http.StatusForbidden, "forbidden: only owners can delete a project")
		}
		p.Obj["status"] = "Terminating"
		p.Obj["deletionTimestamp"] = now()
		p.deleting = s.DeletePolls + 1
		return 200, nil, nil
	})

	// SSH keys
	handle("GET", pid+`/sshkeys`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, list(p.sshKeys, func(k map[string]any) any { return k }), nil
	})
	handle("POST", pid+`/sshkeys`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		pub := strings.TrimSpace(str(b, "spec", "publicKey"))
		fp, ok := fingerprint(pub)
		if !ok {
			return 0, nil, fail(http.StatusBadRequest, "the provided ssh key is invalid due to = ssh: no key found")
		}
		for _, k := range p.sshKeys {
			if k["name"] == b["name"] {
				return 0, nil, fail(http.StatusConflict, "usersshkeys.kubermatic.k8c.io %q already exists", b["name"])
			}
		}
		id := "key-" + strings.ReplaceAll(fp, ":", "")[:8] + "-" + randID(6)
		k := map[string]any{"id": id, "name": b["name"], "creationTimestamp": now(), "spec": map[string]any{"publicKey": pub, "fingerprint": fp}}
		p.sshKeys[id] = k
		return 201, k, nil
	})
	handle("DELETE", pid+`/sshkeys/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if _, ok := p.sshKeys[a[1]]; !ok {
			return 0, nil, fail(http.StatusNotFound, "usersshkeys.kubermatic.k8c.io %q not found", a[1])
		}
		delete(p.sshKeys, a[1])
		for _, c := range p.clusters {
			delete(c.sshKeys, a[1])
		}
		return 200, nil, nil
	})

	// Service accounts
	handle("GET", pid+`/serviceaccounts`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, list(p.sas, func(x map[string]any) any { return x }), nil
	})
	handle("POST", pid+`/serviceaccounts`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		group := str(b, "group")
		if !slices.Contains([]string{"editors", "viewers", "projectmanagers"}, group) {
			return 0, nil, fail(http.StatusBadRequest, "invalid group name %s", group)
		}
		for _, x := range p.sas {
			if x["name"] == b["name"] {
				return 0, nil, fail(http.StatusConflict, "service account %q already exists", b["name"])
			}
		}
		return 201, s.addServiceAccount(p, str(b, "name"), group), nil
	})
	handle("PUT", pid+`/serviceaccounts/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		x, ok := p.sas[a[1]]
		if !ok {
			return 0, nil, fail(http.StatusNotFound, "service account %q not found", a[1])
		}
		x["name"] = b["name"]
		if g := str(b, "group"); g != "" {
			if !strings.HasSuffix(g, "-"+a[0]) {
				g += "-" + a[0]
			}
			x["group"] = g
		}
		return 200, x, nil
	})
	handle("DELETE", pid+`/serviceaccounts/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if _, ok := p.sas[a[1]]; !ok {
			return 0, nil, fail(http.StatusNotFound, "service account %q not found", a[1])
		}
		delete(p.sas, a[1])
		delete(p.saTokens, a[1])
		return 200, nil, nil
	})
	handle("GET", pid+`/serviceaccounts/([^/]+)/tokens`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		toks, ok := p.saTokens[a[1]]
		if !ok {
			return 0, nil, fail(http.StatusNotFound, "service account %q not found", a[1])
		}
		return 200, list(toks, func(x map[string]any) any { return x }), nil
	})
	handle("POST", pid+`/serviceaccounts/([^/]+)/tokens`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if _, ok := p.saTokens[a[1]]; !ok {
			return 0, nil, fail(http.StatusNotFound, "service account %q not found", a[1])
		}
		return 201, s.addToken(p, a[1], str(b, "name")), nil
	})
	handle("PATCH", pid+`/serviceaccounts/([^/]+)/tokens/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		t, ok := p.saTokens[a[1]][a[2]]
		if !ok {
			return 0, nil, fail(http.StatusNotFound, "token %q not found", a[2])
		}
		t["name"] = b["name"]
		return 200, t, nil
	})
	handle("DELETE", pid+`/serviceaccounts/([^/]+)/tokens/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if _, ok := p.saTokens[a[1]][a[2]]; !ok {
			return 0, nil, fail(http.StatusNotFound, "token %q not found", a[2])
		}
		delete(p.saTokens[a[1]], a[2])
		return 200, nil, nil
	})

	// Members
	handle("GET", pid+`/users`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, list(p.members, func(x map[string]any) any { return x }), nil
	})
	memberGroup := func(projectID string, b body) (string, *apiErr) {
		projects, _ := b["projects"].([]any)
		if len(projects) != 1 {
			return "", fail(http.StatusBadRequest, "expected exactly one project in the body")
		}
		pg, _ := projects[0].(map[string]any)
		group := str(pg, "group")
		group = strings.TrimSuffix(group, "-"+projectID)
		if !slices.Contains([]string{"owners", "editors", "viewers", "projectmanagers"}, group) {
			return "", fail(http.StatusBadRequest, "invalid group name %s", group)
		}
		return group + "-" + projectID, nil
	}
	handle("POST", pid+`/users`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		group, e := memberGroup(a[0], b)
		if e != nil {
			return 0, nil, e
		}
		email := str(b, "email")
		for _, m := range p.members {
			if m["email"] == email {
				return 0, nil, fail(http.StatusBadRequest, "cannot add the user = %s to the project %s because user is already in the project", email, a[0])
			}
		}
		id := "user-" + randID(8)
		m := map[string]any{"id": id, "name": strings.Split(email, "@")[0], "email": email, "creationTimestamp": now(),
			"projects": []any{map[string]any{"id": a[0], "group": group}}}
		p.members[id] = m
		return 201, m, nil
	})
	handle("PUT", pid+`/users/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		m, ok := p.members[a[1]]
		if !ok {
			return 0, nil, fail(http.StatusNotFound, "user %q not found", a[1])
		}
		group, e := memberGroup(a[0], b)
		if e != nil {
			return 0, nil, e
		}
		m["projects"] = []any{map[string]any{"id": a[0], "group": group}}
		return 200, m, nil
	})
	handle("DELETE", pid+`/users/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		if _, ok := p.members[a[1]]; !ok {
			return 0, nil, fail(http.StatusNotFound, "user %q not found", a[1])
		}
		delete(p.members, a[1])
		return 200, nil, nil
	})

	// Datacenters and versions
	handle("GET", `/api/v1/dc`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		return 200, Datacenters, nil
	})
	handle("GET", `/api/v1/dc/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		if dc := findDC(a[0]); dc != nil {
			return 200, dc, nil
		}
		return 0, nil, fail(http.StatusNotFound, "datacenter %q not found", a[0])
	})
	handle("GET", `/api/v2/providers/([^/]+)/versions`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		out := []any{}
		for _, v := range Versions {
			out = append(out, map[string]any{"version": v, "default": v == Versions[1]})
		}
		return 200, out, nil
	})

	registerClusterRoutes()
	registerClusterSARoutes()
}

func findDC(name string) map[string]any {
	for _, dc := range Datacenters {
		if str(dc, "metadata", "name") == name {
			return dc
		}
	}
	return nil
}

// tick advances a project's lifecycle: Inactive → Active after one read.
func (s *Server) tick(p *project) {
	if p.Obj["status"] == "Inactive" {
		if p.polls > 0 {
			p.Obj["status"] = "Active"
		}
		p.polls++
	}
}
