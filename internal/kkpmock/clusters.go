package kkpmock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// LastClusterDelete holds the DeleteVolumes/DeleteLoadBalancers headers of the last cluster deletion.
type LastClusterDelete struct {
	ClusterID           string
	DeleteVolumes       string
	DeleteLoadBalancers string
}

// Cluster fields KKP's PATCH handler takes from the patched object (handler/common/cluster.go PatchEndpoint).
var patchableSpec = []string{"cloud", "machineNetworks", "version", "oidc", "auditLogging", "admissionPlugins", "updateWindow",
	"opaIntegration", "podNodeSelectorAdmissionPluginConfig", "eventRateLimitConfig", "serviceAccount", "mla", "containerRuntime",
	"cniPlugin", "exposeStrategy", "backupConfig", "kubernetesDashboard", "apiServerAllowedIPRanges", "kubelb", "disableCsiDriver",
	"kyverno", "encryptionConfiguration", "useEventRateLimitAdmissionPlugin", "usePodNodeSelectorAdmissionPlugin"}

func (s *Server) ready(c *cluster) bool { return c.polls >= s.HealthPolls }

// provider is the cluster's cloud provider and its spec.cloud.<provider> object.
func provider(c *cluster) (string, map[string]any) {
	name := str(c.Obj, "spec", "cloud", "providerName")
	return name, obj(c.Obj, "spec", "cloud", name)
}

// observe is one poll of a cluster: it advances provisioning and, once healthy, fills in the cloud
// resources KKP's cloud controller creates when none were given.
func (s *Server) observe(c *cluster) {
	c.polls++
	if !s.ready(c) {
		return
	}
	name, spec := provider(c)
	if spec == nil || clouds[name].Reconciled == nil {
		return
	}
	for k, v := range clouds[name].Reconciled(str(c.Obj, "id"), findDC(str(c.Obj, "spec", "cloud", "dc"))) {
		if (spec[k] == nil || spec[k] == "") && v != "" {
			spec[k] = v
		}
	}
}

// defaultCloud is KKP's cluster defaulting: NodePorts are open to everyone unless restricted.
func defaultCloud(c *cluster) {
	name, spec := provider(c)
	if spec != nil && clouds[name].NodePorts && spec["nodePortsAllowedIPRanges"] == nil {
		spec["nodePortsAllowedIPRanges"] = map[string]any{"cidrBlocks": []any{"0.0.0.0/0"}}
	}
}

// ospAnnotation holds a machine deployment's operating system profile.
const ospAnnotation = "k8c.io/operating-system-profile"

// reconcileMD adds what KKP and Operating System Manager add to every machine deployment they write: the
// machine fields KKP defaults, system node labels and cloud tags, and the default operating system profile.
func reconcileMD(c *cluster, m map[string]any) {
	tpl := obj(m, "spec", "template")
	prov, clusterCloud := provider(c)
	node, mc, id := obj(tpl, "cloud", prov), clouds[prov], str(c.Obj, "id")
	if mc.NodeDefaults != nil {
		for k, v := range mc.NodeDefaults(findDC(str(c.Obj, "spec", "cloud", "dc")), clusterCloud, osName(tpl)) {
			if (node[k] == nil || node[k] == "") && v != "" {
				node[k] = v
			}
		}
	}
	if mc.SystemTags != nil {
		tags := obj(node, "tags")
		if tags == nil {
			tags = map[string]any{}
			node["tags"] = tags
		}
		maps.Copy(tags, mc.SystemTags(id, c.projectID))
	}
	if mc.SystemTagsList != nil {
		tags := mc.SystemTagsList(id, c.projectID)
		if l, ok := node["tags"].([]any); ok {
			for _, t := range l {
				tags = append(tags, fmt.Sprint(t))
			}
		}
		slices.Sort(tags)
		var out []any
		for _, t := range slices.Compact(tags) {
			out = append(out, t)
		}
		node["tags"] = out
	}
	labels := obj(tpl, "labels")
	if labels == nil {
		labels = map[string]any{}
		tpl["labels"] = labels
	}
	labels["system/cluster"], labels["system/project"] = id, c.projectID
	if ann := obj(m, "annotations"); str(ann, ospAnnotation) == "" {
		ann[ospAnnotation] = "osp-" + osName(tpl)
	}
}

func stripCredentials(c *cluster) {
	name, spec := provider(c)
	if spec == nil {
		return
	}
	had := false
	for _, f := range clouds[name].Secret {
		if v, ok := spec[f]; ok && v != "" && v != false {
			had = true
		}
		delete(spec, f)
	}
	if had || spec["credentialsReference"] == nil {
		spec["credentialsReference"] = map[string]any{"name": "credential-" + name + "-" + str(c.Obj, "id"), "namespace": "kubermatic"}
	}
}

// mdView is one poll of a machine deployment: machines come up one poll after a create or change.
func (s *Server) mdView(m *md) map[string]any {
	if m.deleting == 0 {
		r := obj(m.Obj, "spec")["replicas"]
		m.Obj["status"] = map[string]any{"replicas": r, "updatedReplicas": r, "readyReplicas": r, "availableReplicas": r}
	}
	return m.Obj
}

func checkKubelet(kubelet, controlPlane string) *apiErr {
	if kubelet == "" {
		return fail(http.StatusBadRequest, "failed to parse kubelet version: Invalid Semantic Version")
	}
	if versionLess(controlPlane, kubelet) {
		return fail(http.StatusBadRequest, "kubelet version %s is not compatible with control plane version %s: kubelet cannot be newer than the control plane", kubelet, controlPlane)
	}
	if minor(controlPlane)-minor(kubelet) > 3 {
		return fail(http.StatusBadRequest, "kubelet version %s is not compatible with control plane version %s: maximum 3 minor versions skew", kubelet, controlPlane)
	}
	return nil
}

func osName(tpl map[string]any) string {
	for k := range obj(tpl, "operatingSystem") {
		return k
	}
	return ""
}

func osCount(tpl map[string]any) int {
	n := 0
	for _, k := range []string{"ubuntu", "flatcar", "rhel", "rockylinux", "amzn2"} {
		if obj(tpl, "operatingSystem", k) != nil {
			n++
		}
	}
	return n
}

func registerClusterRoutes() {
	clusterOf := func(s *Server, a args, sa string) (*project, *cluster, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return nil, nil, e
		}
		c, e := s.cluster(p, a[1])
		return p, c, e
	}

	handle("GET", `/api/v2/projects/([^/]+)/clusters`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, list(p.clusters, func(c *cluster) any { return c.Obj }), nil
	})
	handle("POST", `/api/v2/projects/([^/]+)/clusters`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, e := s.project(a[0], sa)
		if e != nil {
			return 0, nil, e
		}
		in := obj(b, "cluster")
		if in == nil {
			return 0, nil, fail(http.StatusBadRequest, "cluster is required")
		}
		if str(in, "name") == "" {
			return 0, nil, fail(http.StatusBadRequest, "invalid cluster: invalid cloud spec \"name\" is required")
		}
		dcName := str(in, "spec", "cloud", "dc")
		dc := findDC(dcName)
		if dc == nil {
			return 0, nil, fail(http.StatusBadRequest, "invalid cluster: invalid cloud spec: unknown datacenter %q", dcName)
		}
		version := str(in, "spec", "version")
		if !slices.Contains(Versions, version) {
			return 0, nil, fail(http.StatusBadRequest, "invalid cluster: version %q is not supported, supported versions: %s", version, strings.Join(Versions, ", "))
		}
		prov := str(dc, "spec", "provider")
		cloud, known := clouds[prov]
		cs := obj(in, "spec", "cloud", prov)
		if !known {
			return 0, nil, fail(http.StatusBadRequest, "invalid cluster: the mock does not support %s datacenters", prov)
		}
		if cs == nil {
			return 0, nil, fail(http.StatusBadRequest, "invalid cluster: invalid cloud spec: datacenter %q is a %s datacenter, but spec.cloud.%s is not set", dcName, prov, prov)
		}
		for k := range clouds {
			if k != prov && obj(in, "spec", "cloud", k) != nil {
				return 0, nil, fail(http.StatusBadRequest, "invalid cluster: invalid cloud spec: only one cloud provider can be set, got %s and %s", prov, k)
			}
		}
		if preset := str(in, "credential"); preset != "" {
			if Presets[preset] != prov {
				return 0, nil, fail(http.StatusBadRequest, "can not get preset %s for user admin@example.com: presets.kubermatic.k8c.io %q not found", preset, preset)
			}
		} else if !cloud.validCredentials(cs) {
			return 0, nil, fail(http.StatusBadRequest, "invalid credentials: %s required", cloud.credentialHelp())
		} else if prov == "gcp" {
			// KKP base64-decodes the service account key and parses it as JSON.
			raw, err := base64.StdEncoding.DecodeString(str(cs, "serviceAccount"))
			if err != nil || !json.Valid(raw) {
				return 0, nil, fail(http.StatusBadRequest, "invalid credentials: serviceAccount must be a base64 encoded JSON key")
			}
		}

		id := randID(10)
		spec := clone(obj(in, "spec"))
		obj(spec, "cloud")["providerName"] = prov
		defaults := map[string]any{
			"containerRuntime": "containerd", "exposeStrategy": "NodePort", "enableUserSSHKeyAgent": true,
			"kubernetesDashboard": map[string]any{"enabled": true}, "auditLogging": map[string]any{"enabled": false},
			"cniPlugin": map[string]any{"type": "cilium", "version": "1.16.6"},
		}
		for k, v := range defaults {
			if spec[k] == nil {
				spec[k] = v
			}
		}
		if obj(spec, "cniPlugin")["type"] == "cilium" && str(spec, "cniPlugin", "version") == "" {
			obj(spec, "cniPlugin")["version"] = "1.16.6"
		}
		net := obj(spec, "clusterNetwork")
		if net == nil {
			net = map[string]any{}
			spec["clusterNetwork"] = net
		}
		proxy := "ipvs"
		if str(spec, "cniPlugin", "type") == "cilium" {
			proxy = "ebpf"
		}
		for k, v := range map[string]any{"ipFamily": "IPv4", "pods": map[string]any{"cidrBlocks": []any{"172.25.0.0/16"}},
			"services": map[string]any{"cidrBlocks": []any{"10.240.16.0/20"}}, "proxyMode": proxy, "dnsDomain": "cluster.local",
			"nodeCidrMaskSizeIPv4": 24, "nodeLocalDNSCacheEnabled": true, "konnectivityEnabled": true} {
			if net[k] == nil {
				net[k] = v
			}
		}
		labels := obj(in, "labels")
		if len(s.ClusterLabels) > 0 {
			if labels = clone(labels); labels == nil {
				labels = map[string]any{}
			}
			for k, v := range s.ClusterLabels {
				labels[k] = v
			}
		}
		annotations := map[string]any{"kubermatic.io/mock-reconciled": "true"}
		for k, v := range obj(in, "annotations") {
			annotations[k] = v
		}
		c := &cluster{projectID: str(p.Obj, "id"), sshKeys: map[string]bool{}, mds: map[string]*md{}, sas: map[string]*clusterSA{}, bindings: newBindings()}
		c.Obj = map[string]any{"id": id, "name": in["name"], "type": "kubernetes", "creationTimestamp": now(),
			"annotations": annotations, "spec": spec,
			"status": map[string]any{"version": version, "url": fmt.Sprintf("https://%s.europe.kkp.example.com:6443", id)}}
		if len(labels) > 0 {
			c.Obj["labels"] = labels
		}
		if nd := obj(b, "nodeDeployment"); nd != nil {
			return 0, nil, fail(http.StatusNotImplemented, "the mock does not create initial node deployments")
		}
		defaultCloud(c)
		stripCredentials(c)
		p.clusters[id] = c
		return 201, c.Obj, nil
	})
	handle("GET", cid, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		s.observe(c)
		return 200, c.Obj, nil
	})
	handle("GET", cid+`/health`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		s.observe(c)
		st := "HealthStatusProvisioning"
		if s.ready(c) {
			st = "HealthStatusUp"
		}
		h := map[string]any{}
		for _, k := range []string{"apiserver", "scheduler", "controller", "machineController", "etcd", "cloudProviderInfrastructure", "userClusterControllerManager", "operatingSystemManager"} {
			h[k] = st
		}
		h["etcd"] = "HealthStatusUp"
		return 200, h, nil
	})
	handle("PATCH", cid, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/merge-patch+json" && ct != "application/json" {
			return 0, nil, fail(http.StatusUnsupportedMediaType, "unsupported content type %q", ct)
		}
		patched := mergePatch(clone(c.Obj), b)
		oldSpec, newSpec := obj(c.Obj, "spec"), obj(patched, "spec")
		if str(newSpec, "cloud", "dc") != str(oldSpec, "cloud", "dc") {
			return 0, nil, fail(http.StatusBadRequest, "changing the datacenter is not allowed")
		}
		if t := str(newSpec, "cniPlugin", "type"); t != str(oldSpec, "cniPlugin", "type") {
			return 0, nil, fail(http.StatusBadRequest, "cannot change CNI plugin type")
		}
		oldV, newV := str(oldSpec, "version"), str(newSpec, "version")
		if newV != oldV {
			if !slices.Contains(Versions, newV) {
				return 0, nil, fail(http.StatusBadRequest, "invalid cluster: version %q is not supported", newV)
			}
			if versionLess(newV, oldV) {
				return 0, nil, fail(http.StatusBadRequest, "invalid cluster: downgrading the control plane is not supported")
			}
			if minor(newV)-minor(oldV) > 1 {
				return 0, nil, fail(http.StatusBadRequest, "invalid cluster: cannot upgrade more than one minor version at a time")
			}
			for _, m := range c.mds {
				if kv := str(m.Obj, "spec", "template", "versions", "kubelet"); minor(newV)-minor(kv) > 3 {
					return 0, nil, fail(http.StatusBadRequest, "Cluster contains nodes running the following incompatible kubelet versions: [%s]. Upgrade your nodes before you upgrade the cluster.", kv)
				}
			}
		}
		for _, k := range patchableSpec {
			if v, ok := newSpec[k]; ok {
				oldSpec[k] = v
			} else {
				delete(oldSpec, k)
			}
		}
		if net := obj(newSpec, "clusterNetwork"); net != nil {
			for _, k := range []string{"konnectivityEnabled", "proxyMode"} {
				obj(oldSpec, "clusterNetwork")[k] = net[k]
			}
		}
		c.Obj["name"] = patched["name"]
		for _, k := range []string{"labels", "annotations"} {
			if v := obj(patched, k); len(v) > 0 {
				c.Obj[k] = v
			} else {
				delete(c.Obj, k)
			}
		}
		obj(c.Obj, "status")["version"] = str(oldSpec, "version")
		defaultCloud(c)
		stripCredentials(c)
		return 200, c.Obj, nil
	})
	handle("DELETE", cid, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		s.LastDelete = LastClusterDelete{ClusterID: a[1], DeleteVolumes: r.Header.Get("DeleteVolumes"), DeleteLoadBalancers: r.Header.Get("DeleteLoadBalancers")}
		if c.deleting == 0 {
			c.Obj["deletionTimestamp"] = now()
			c.deleting = s.DeletePolls + 1
		}
		return 200, nil, nil
	})
	handle("GET", cid+`/kubeconfig`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
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
    user: default
  name: default
current-context: default
users:
- name: default
  user:
    token: mock-admin-token.%s
`, str(c.Obj, "status", "url"), id, id, id)), nil
	})
	handle("GET", cid+`/upgrades`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		cur := str(c.Obj, "spec", "version")
		var out []any
		for _, v := range Versions {
			if versionLess(cur, v) && minor(v)-minor(cur) <= 1 {
				out = append(out, map[string]any{"version": v})
			}
		}
		return 200, out, nil
	})

	// Cluster SSH keys
	handle("GET", cid+`/sshkeys`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		keys := map[string]map[string]any{}
		for id := range c.sshKeys {
			keys[id] = p.sshKeys[id]
		}
		return 200, list(keys, func(k map[string]any) any { return k }), nil
	})
	handle("PUT", cid+`/sshkeys/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		p, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		if _, ok := p.sshKeys[a[2]]; !ok {
			return 0, nil, fail(http.StatusNotFound, "usersshkeys.kubermatic.k8c.io %q not found", a[2])
		}
		c.sshKeys[a[2]] = true
		return 201, nil, nil
	})
	handle("DELETE", cid+`/sshkeys/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		if !c.sshKeys[a[2]] {
			return 0, nil, fail(http.StatusNotFound, "the sshkey with id %s is not assigned to the cluster", a[2])
		}
		delete(c.sshKeys, a[2])
		return 200, nil, nil
	})

	// Machine deployments
	mdOf := func(s *Server, a args, sa string) (*cluster, *md, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return nil, nil, e
		}
		m, ok := c.mds[a[2]]
		if ok && m.deleting > 0 {
			m.deleting--
			if m.deleting == 0 {
				delete(c.mds, a[2])
				ok = false
			}
		}
		if !ok {
			return nil, nil, fail(http.StatusNotFound, "machinedeployments.cluster.k8s.io %q not found", a[2])
		}
		return c, m, nil
	}
	handle("GET", cid+`/machinedeployments`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, list(c.mds, func(m *md) any { return s.mdView(m) }), nil
	})
	handle("POST", cid+`/machinedeployments`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, c, e := clusterOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		if !s.ready(c) {
			s.observe(c) // time passes while the client retries
			return 0, nil, fail(http.StatusServiceUnavailable, "Cluster components are not ready yet")
		}
		name := str(b, "name")
		if !dnsLabel.MatchString(name) {
			return 0, nil, fail(http.StatusBadRequest, "node deployment validation failed: invalid name %q", name)
		}
		if _, ok := c.mds[name]; ok {
			return 0, nil, fail(http.StatusConflict, "machinedeployments.cluster.k8s.io %q already exists", name)
		}
		spec := clone(obj(b, "spec"))
		tpl := obj(spec, "template")
		prov, _ := provider(c)
		node := obj(tpl, "cloud", prov)
		if tpl == nil || node == nil || len(obj(tpl, "cloud")) != 1 {
			return 0, nil, fail(http.StatusBadRequest, "node deployment validation failed: the node spec must be for the cluster's cloud provider, %s", prov)
		}
		for _, k := range clouds[prov].NodeRequired {
			if node[k] == nil || node[k] == "" || node[k] == float64(0) {
				return 0, nil, fail(http.StatusBadRequest, "node deployment validation failed: %s.%s is required", prov, k)
			}
		}
		if osCount(tpl) != 1 {
			return 0, nil, fail(http.StatusBadRequest, "node deployment validation failed: exactly one operating system is required")
		}
		cp := str(c.Obj, "spec", "version")
		if obj(tpl, "versions") == nil {
			tpl["versions"] = map[string]any{}
		}
		if str(tpl, "versions", "kubelet") == "" {
			obj(tpl, "versions")["kubelet"] = cp
		}
		if e := checkKubelet(str(tpl, "versions", "kubelet"), cp); e != nil {
			return 0, nil, e
		}
		if e := checkReplicas(spec); e != nil {
			return 0, nil, e
		}
		annotations := map[string]any{"machinedeployment.clusters.k8s.io/revision": "1"}
		for k, v := range obj(b, "annotations") {
			annotations[k] = v
		}
		m := &md{Obj: map[string]any{"id": name, "name": name, "creationTimestamp": now(), "annotations": annotations, "spec": spec,
			"status": map[string]any{"replicas": spec["replicas"]}}}
		reconcileMD(c, m.Obj)
		c.mds[name] = m
		return 201, m.Obj, nil
	})
	handle("GET", cid+`/machinedeployments/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, m, e := mdOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		return 200, s.mdView(m), nil
	})
	handle("PATCH", cid+`/machinedeployments/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		c, m, e := mdOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		base := clone(m.Obj)
		// Like upstream: an operating system in the patch replaces the old one instead of merging with it.
		if osCount(obj(b, "spec", "template")) == 1 {
			delete(obj(base, "spec", "template"), "operatingSystem")
		}
		patched := mergePatch(base, b)
		spec := obj(patched, "spec")
		if osCount(obj(spec, "template")) != 1 {
			return 0, nil, fail(http.StatusBadRequest, "cannot have more than one os")
		}
		if e := checkKubelet(str(spec, "template", "versions", "kubelet"), str(c.Obj, "spec", "version")); e != nil {
			return 0, nil, e
		}
		if e := checkReplicas(spec); e != nil {
			return 0, nil, e
		}
		m.Obj["spec"] = spec
		ann := obj(patched, "annotations")
		if ann == nil {
			ann = map[string]any{}
		}
		rev, _ := strconv.Atoi(str(m.Obj, "annotations", "machinedeployment.clusters.k8s.io/revision"))
		ann["machinedeployment.clusters.k8s.io/revision"] = strconv.Itoa(rev + 1)
		m.Obj["annotations"] = ann
		reconcileMD(c, m.Obj)
		m.Obj["status"] = map[string]any{"replicas": spec["replicas"]}
		return 200, m.Obj, nil
	})
	handle("DELETE", cid+`/machinedeployments/([^/]+)`, func(s *Server, r *http.Request, a args, b body, sa string) (int, any, *apiErr) {
		_, m, e := mdOf(s, a, sa)
		if e != nil {
			return 0, nil, e
		}
		if m.deleting == 0 {
			m.Obj["deletionTimestamp"] = now()
			m.deleting = s.DeletePolls + 1
		}
		return 200, nil, nil
	})
}

func checkReplicas(spec map[string]any) *apiErr {
	num := func(k string) (int, bool) {
		f, ok := spec[k].(float64)
		return int(f), ok
	}
	replicas, _ := num("replicas")
	if replicas < 0 {
		return fail(http.StatusBadRequest, "replicas cannot be negative")
	}
	if maxR, ok := num("maxReplicas"); ok && replicas > maxR {
		return fail(http.StatusBadRequest, "replica count (%d) cannot be higher then autoscaler maxreplicas (%d)", replicas, maxR)
	}
	if minR, ok := num("minReplicas"); ok && replicas < minR {
		return fail(http.StatusBadRequest, "replica count (%d) cannot be lower then autoscaler minreplicas (%d)", replicas, minR)
	}
	return nil
}
