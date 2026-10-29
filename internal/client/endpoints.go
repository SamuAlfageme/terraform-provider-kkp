package client

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// Version is GET /api/v1/version.
type Version struct {
	API string `json:"api"`
}

func (c *Client) Version(ctx context.Context) (*Version, error) {
	var v Version
	return &v, c.get(ctx, "/api/v1/version", &v)
}

// Projects

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	return out, c.get(ctx, "/api/v1/projects", &out)
}

func (c *Client) GetProject(ctx context.Context, id string) (*Project, error) {
	var out Project
	return &out, c.get(ctx, "/api/v1/projects/"+esc(id), &out)
}

// CreateProject needs a user token: KKP answers 403 "the Service Account is not allowed to create a project"
// for service account tokens.
func (c *Client) CreateProject(ctx context.Context, body CreateProjectBody) (*Project, error) {
	var out Project
	return &out, c.post(ctx, "/api/v1/projects", body, &out)
}

// UpdateProject is a PUT of the whole project; KKP takes name and labels from it.
func (c *Client) UpdateProject(ctx context.Context, p Project) (*Project, error) {
	var out Project
	return &out, c.put(ctx, "/api/v1/projects/"+esc(p.ID), p, &out)
}

func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.delete(ctx, "/api/v1/projects/"+esc(id), nil)
}

// SSH keys

func (c *Client) ListSSHKeys(ctx context.Context, projectID string) ([]SSHKey, error) {
	var out []SSHKey
	return out, c.get(ctx, fmt.Sprintf("/api/v1/projects/%s/sshkeys", esc(projectID)), &out)
}

// GetSSHKey lists and filters: KKP has no GET for a single key.
func (c *Client) GetSSHKey(ctx context.Context, projectID, id string) (*SSHKey, error) {
	keys, err := c.ListSSHKeys(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].ID == id {
			return &keys[i], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: "sshkeys/" + id, StatusCode: http.StatusNotFound, Message: "SSH key not found"}
}

func (c *Client) CreateSSHKey(ctx context.Context, projectID, name, publicKey string) (*SSHKey, error) {
	var out SSHKey
	body := SSHKey{ObjectMeta: ObjectMeta{Name: name}, Spec: SSHKeySpec{PublicKey: publicKey}}
	return &out, c.post(ctx, fmt.Sprintf("/api/v1/projects/%s/sshkeys", esc(projectID)), body, &out)
}

func (c *Client) DeleteSSHKey(ctx context.Context, projectID, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/projects/%s/sshkeys/%s", esc(projectID), esc(id)), nil)
}

// Service accounts and tokens

func (c *Client) ListServiceAccounts(ctx context.Context, projectID string) ([]ServiceAccount, error) {
	var out []ServiceAccount
	return out, c.get(ctx, fmt.Sprintf("/api/v1/projects/%s/serviceaccounts", esc(projectID)), &out)
}

// GetServiceAccount lists and filters: KKP has no GET for a single service account.
func (c *Client) GetServiceAccount(ctx context.Context, projectID, id string) (*ServiceAccount, error) {
	sas, err := c.ListServiceAccounts(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range sas {
		if sas[i].ID == id {
			return &sas[i], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: "serviceaccounts/" + id, StatusCode: http.StatusNotFound, Message: "service account not found"}
}

func (c *Client) CreateServiceAccount(ctx context.Context, projectID, name, group string) (*ServiceAccount, error) {
	var out ServiceAccount
	body := ServiceAccount{ObjectMeta: ObjectMeta{Name: name}, Group: group}
	return &out, c.post(ctx, fmt.Sprintf("/api/v1/projects/%s/serviceaccounts", esc(projectID)), body, &out)
}

func (c *Client) UpdateServiceAccount(ctx context.Context, projectID string, sa ServiceAccount) (*ServiceAccount, error) {
	var out ServiceAccount
	return &out, c.put(ctx, fmt.Sprintf("/api/v1/projects/%s/serviceaccounts/%s", esc(projectID), esc(sa.ID)), sa, &out)
}

func (c *Client) DeleteServiceAccount(ctx context.Context, projectID, id string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/projects/%s/serviceaccounts/%s", esc(projectID), esc(id)), nil)
}

func tokensPath(projectID, saID string) string {
	return fmt.Sprintf("/api/v1/projects/%s/serviceaccounts/%s/tokens", esc(projectID), esc(saID))
}

func (c *Client) ListServiceAccountTokens(ctx context.Context, projectID, saID string) ([]ServiceAccountToken, error) {
	var out []ServiceAccountToken
	return out, c.get(ctx, tokensPath(projectID, saID), &out)
}

func (c *Client) GetServiceAccountToken(ctx context.Context, projectID, saID, id string) (*ServiceAccountToken, error) {
	tokens, err := c.ListServiceAccountTokens(ctx, projectID, saID)
	if err != nil {
		return nil, err
	}
	for i := range tokens {
		if tokens[i].ID == id {
			return &tokens[i], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: "tokens/" + id, StatusCode: http.StatusNotFound, Message: "token not found"}
}

// CreateServiceAccountToken returns the secret; it is the only time KKP shows it.
func (c *Client) CreateServiceAccountToken(ctx context.Context, projectID, saID, name string) (*ServiceAccountToken, error) {
	var out ServiceAccountToken
	return &out, c.post(ctx, tokensPath(projectID, saID), ServiceAccountToken{ObjectMeta: ObjectMeta{Name: name}}, &out)
}

func (c *Client) RenameServiceAccountToken(ctx context.Context, projectID, saID, id, name string) (*ServiceAccountToken, error) {
	var out ServiceAccountToken
	return &out, c.mergePatch(ctx, tokensPath(projectID, saID)+"/"+esc(id), map[string]string{"name": name}, &out)
}

func (c *Client) DeleteServiceAccountToken(ctx context.Context, projectID, saID, id string) error {
	return c.delete(ctx, tokensPath(projectID, saID)+"/"+esc(id), nil)
}

// Project members

func (c *Client) ListProjectMembers(ctx context.Context, projectID string) ([]User, error) {
	var out []User
	return out, c.get(ctx, fmt.Sprintf("/api/v1/projects/%s/users", esc(projectID)), &out)
}

func (c *Client) AddProjectMember(ctx context.Context, projectID, email, group string) (*User, error) {
	var out User
	body := User{Email: email, Projects: []ProjectGroup{{ID: projectID, GroupPrefix: group}}}
	return &out, c.post(ctx, fmt.Sprintf("/api/v1/projects/%s/users", esc(projectID)), body, &out)
}

func (c *Client) UpdateProjectMember(ctx context.Context, projectID string, u User, group string) (*User, error) {
	var out User
	u.Projects = []ProjectGroup{{ID: projectID, GroupPrefix: group}}
	return &out, c.put(ctx, fmt.Sprintf("/api/v1/projects/%s/users/%s", esc(projectID), esc(u.ID)), u, &out)
}

func (c *Client) RemoveProjectMember(ctx context.Context, projectID, userID string) error {
	return c.delete(ctx, fmt.Sprintf("/api/v1/projects/%s/users/%s", esc(projectID), esc(userID)), nil)
}

// Datacenters and versions

func (c *Client) ListDatacenters(ctx context.Context) ([]Datacenter, error) {
	var out []Datacenter
	return out, c.get(ctx, "/api/v1/dc", &out)
}

func (c *Client) GetDatacenter(ctx context.Context, name string) (*Datacenter, error) {
	var out Datacenter
	return &out, c.get(ctx, "/api/v1/dc/"+esc(name), &out)
}

// ListVersions lists the control plane versions KKP offers for a provider (e.g. "openstack").
func (c *Client) ListVersions(ctx context.Context, provider string) ([]MasterVersion, error) {
	var out []MasterVersion
	return out, c.get(ctx, "/api/v2/providers/"+esc(provider)+"/versions", &out)
}

// Clusters

func clustersPath(projectID string) string {
	return fmt.Sprintf("/api/v2/projects/%s/clusters", esc(projectID))
}

func clusterPath(projectID, clusterID string) string {
	return clustersPath(projectID) + "/" + esc(clusterID)
}

func (c *Client) ListClusters(ctx context.Context, projectID string) ([]Cluster, error) {
	var out []Cluster
	return out, c.get(ctx, clustersPath(projectID), &out)
}

func (c *Client) GetCluster(ctx context.Context, projectID, clusterID string) (*Cluster, error) {
	var out Cluster
	return &out, c.get(ctx, clusterPath(projectID, clusterID), &out)
}

func (c *Client) CreateCluster(ctx context.Context, projectID string, body CreateClusterBody) (*Cluster, error) {
	var out Cluster
	return &out, c.post(ctx, clustersPath(projectID), body, &out)
}

// PatchCluster sends a JSON merge patch. KKP applies it to the cluster and then copies only some fields
// over (name, labels, annotations, cloud, version, audit logging, admission plugins, OPA, MLA, CNI,
// expose strategy, API server allowed IPs, Kubernetes dashboard, Kyverno, …); changes to anything else,
// such as the datacenter or the pod CIDRs, are silently dropped, which is why the provider replaces
// the cluster for those.
func (c *Client) PatchCluster(ctx context.Context, projectID, clusterID string, patch any) (*Cluster, error) {
	var out Cluster
	return &out, c.mergePatch(ctx, clusterPath(projectID, clusterID), patch, &out)
}

// DeleteCluster asks KKP to delete the cluster; deletion finishes asynchronously.
func (c *Client) DeleteCluster(ctx context.Context, projectID, clusterID string, deleteVolumes, deleteLoadBalancers bool) error {
	return c.delete(ctx, clusterPath(projectID, clusterID), map[string]string{
		"DeleteVolumes":       strconv.FormatBool(deleteVolumes),
		"DeleteLoadBalancers": strconv.FormatBool(deleteLoadBalancers),
	})
}

func (c *Client) GetClusterHealth(ctx context.Context, projectID, clusterID string) (*ClusterHealth, error) {
	var out ClusterHealth
	return &out, c.get(ctx, clusterPath(projectID, clusterID)+"/health", &out)
}

// GetAdminKubeconfig returns the cluster's admin kubeconfig as YAML.
func (c *Client) GetAdminKubeconfig(ctx context.Context, projectID, clusterID string) (string, error) {
	var raw []byte
	err := c.do(ctx, request{method: http.MethodGet, path: clusterPath(projectID, clusterID) + "/kubeconfig"}, &raw)
	return string(raw), err
}

func (c *Client) ListClusterSSHKeys(ctx context.Context, projectID, clusterID string) ([]SSHKey, error) {
	var out []SSHKey
	return out, c.get(ctx, clusterPath(projectID, clusterID)+"/sshkeys", &out)
}

func (c *Client) AssignClusterSSHKey(ctx context.Context, projectID, clusterID, keyID string) error {
	return c.put(ctx, clusterPath(projectID, clusterID)+"/sshkeys/"+esc(keyID), nil, nil)
}

// DetachClusterSSHKey uses the /api/v2 route the server registers; swagger.json lists it without /v2.
func (c *Client) DetachClusterSSHKey(ctx context.Context, projectID, clusterID, keyID string) error {
	return c.delete(ctx, clusterPath(projectID, clusterID)+"/sshkeys/"+esc(keyID), nil)
}

// Upgrades lists the versions a cluster can be upgraded to.
func (c *Client) ClusterUpgrades(ctx context.Context, projectID, clusterID string) ([]MasterVersion, error) {
	var out []MasterVersion
	return out, c.get(ctx, clusterPath(projectID, clusterID)+"/upgrades", &out)
}

// Machine deployments

func mdsPath(projectID, clusterID string) string {
	return clusterPath(projectID, clusterID) + "/machinedeployments"
}

func (c *Client) ListMachineDeployments(ctx context.Context, projectID, clusterID string) ([]NodeDeployment, error) {
	var out []NodeDeployment
	return out, c.get(ctx, mdsPath(projectID, clusterID), &out)
}

func (c *Client) GetMachineDeployment(ctx context.Context, projectID, clusterID, id string) (*NodeDeployment, error) {
	var out NodeDeployment
	return &out, c.get(ctx, mdsPath(projectID, clusterID)+"/"+esc(id), &out)
}

// CreateMachineDeployment answers 503 until the cluster's control plane is healthy.
func (c *Client) CreateMachineDeployment(ctx context.Context, projectID, clusterID string, md NodeDeployment) (*NodeDeployment, error) {
	var out NodeDeployment
	return &out, c.post(ctx, mdsPath(projectID, clusterID), md, &out)
}

func (c *Client) PatchMachineDeployment(ctx context.Context, projectID, clusterID, id string, patch any) (*NodeDeployment, error) {
	var out NodeDeployment
	return &out, c.mergePatch(ctx, mdsPath(projectID, clusterID)+"/"+esc(id), patch, &out)
}

func (c *Client) DeleteMachineDeployment(ctx context.Context, projectID, clusterID, id string) error {
	return c.delete(ctx, mdsPath(projectID, clusterID)+"/"+esc(id), nil)
}

// RestartMachineDeployment rolls every machine of the deployment (POST on the deployment).
func (c *Client) RestartMachineDeployment(ctx context.Context, projectID, clusterID, id string) error {
	return c.post(ctx, mdsPath(projectID, clusterID)+"/"+esc(id), nil, nil)
}

// Cluster service accounts: Kubernetes service accounts in a user cluster, created and bound through the KKP API,
// so they work where admin kubeconfigs are disabled.

func clusterSAsPath(projectID, clusterID string) string {
	return clusterPath(projectID, clusterID) + "/serviceaccount"
}

func clusterSAPath(projectID, clusterID, namespace, name string) string {
	return clusterSAsPath(projectID, clusterID) + "/" + esc(namespace) + "/" + esc(name)
}

// ListClusterServiceAccounts lists the service accounts KKP created (labelled component=clusterServiceAccount).
func (c *Client) ListClusterServiceAccounts(ctx context.Context, projectID, clusterID string) ([]ClusterServiceAccount, error) {
	var out []ClusterServiceAccount
	return out, c.get(ctx, clusterSAsPath(projectID, clusterID), &out)
}

// GetClusterServiceAccount lists and filters: KKP has no GET for a single cluster service account.
func (c *Client) GetClusterServiceAccount(ctx context.Context, projectID, clusterID, namespace, name string) (*ClusterServiceAccount, error) {
	sas, err := c.ListClusterServiceAccounts(ctx, projectID, clusterID)
	if err != nil {
		return nil, err
	}
	for i := range sas {
		if sas[i].Namespace == namespace && sas[i].Name == name {
			return &sas[i], nil
		}
	}
	return nil, &APIError{Method: http.MethodGet, Path: "serviceaccount/" + namespace + "/" + name, StatusCode: http.StatusNotFound, Message: "cluster service account not found"}
}

// CreateClusterServiceAccount creates the service account and a token secret for it in the user cluster.
func (c *Client) CreateClusterServiceAccount(ctx context.Context, projectID, clusterID, namespace, name string) (*ClusterServiceAccount, error) {
	var out ClusterServiceAccount
	body := ClusterServiceAccount{ObjectMeta: ObjectMeta{Name: name}, Namespace: namespace}
	return &out, c.post(ctx, clusterSAsPath(projectID, clusterID), body, &out)
}

// DeleteClusterServiceAccount removes the service account from every KKP binding, then deletes it.
func (c *Client) DeleteClusterServiceAccount(ctx context.Context, projectID, clusterID, namespace, name string) error {
	return c.delete(ctx, clusterSAPath(projectID, clusterID, namespace, name), nil)
}

// GetClusterServiceAccountKubeconfig returns a kubeconfig YAML with the service account's token. It fails until
// the token controller has filled in the token secret.
func (c *Client) GetClusterServiceAccountKubeconfig(ctx context.Context, projectID, clusterID, namespace, name string) (string, error) {
	var raw []byte
	err := c.do(ctx, request{method: http.MethodGet, path: clusterSAPath(projectID, clusterID, namespace, name) + "/kubeconfig"}, &raw)
	return string(raw), err
}

// GetClusterServiceAccountPermissions lists the KKP bindings (cluster roles and roles) of the service account.
func (c *Client) GetClusterServiceAccountPermissions(ctx context.Context, projectID, clusterID, namespace, name string) ([]Permission, error) {
	var out []Permission
	return out, c.get(ctx, clusterSAPath(projectID, clusterID, namespace, name)+"/permissions", &out)
}

func clusterBindingsPath(projectID, clusterID, role string) string {
	return clusterPath(projectID, clusterID) + "/clusterroles/" + esc(role) + "/clusterbindings"
}

// BindClusterRole adds a subject to the cluster role's KKP binding. KKP answers 400 if it is already bound, and
// 500 "the cluster role binding not found" for a role KKP does not manage bindings for.
func (c *Client) BindClusterRole(ctx context.Context, projectID, clusterID, role string, subject ClusterRoleUser) error {
	return c.post(ctx, clusterBindingsPath(projectID, clusterID, role), subject, nil)
}

// UnbindClusterRole removes a subject from the cluster role's KKP binding (a DELETE with a body).
func (c *Client) UnbindClusterRole(ctx context.Context, projectID, clusterID, role string, subject ClusterRoleUser) error {
	return c.do(ctx, request{method: http.MethodDelete, path: clusterBindingsPath(projectID, clusterID, role), body: subject}, nil)
}
