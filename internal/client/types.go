package client

import "encoding/json"

// API types, trimmed to the fields the provider reads or writes. Field names follow KKP's JSON.

type ObjectMeta struct {
	ID                string            `json:"id,omitempty"`
	Name              string            `json:"name,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

// Project statuses. KKP creates projects as Inactive and its controller flips them to Active.
const (
	ProjectActive      = "Active"
	ProjectInactive    = "Inactive"
	ProjectTerminating = "Terminating"
)

type ProjectOwner struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type Project struct {
	ObjectMeta
	Status         string         `json:"status,omitempty"`
	Owners         []ProjectOwner `json:"owners,omitempty"`
	ClustersNumber int            `json:"clustersNumber,omitempty"`
}

type CreateProjectBody struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

type SSHKey struct {
	ObjectMeta
	Spec SSHKeySpec `json:"spec"`
}

type SSHKeySpec struct {
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"publicKey"`
}

type ServiceAccount struct {
	ObjectMeta
	// Group is e.g. "editors" on create; KKP answers with "editors-<project id>".
	Group  string `json:"group"`
	Status string `json:"status,omitempty"`
}

type ServiceAccountToken struct {
	ObjectMeta
	Expiry      string `json:"expiry,omitempty"`
	Invalidated bool   `json:"invalidated,omitempty"`
	// Token is only present in the answer to create and regenerate.
	Token string `json:"token,omitempty"`
}

type ProjectGroup struct {
	ID string `json:"id"`
	// Group is e.g. "viewers" on write; KKP answers with "viewers-<project id>".
	GroupPrefix string `json:"group"`
}

type User struct {
	ObjectMeta
	Email    string         `json:"email"`
	Projects []ProjectGroup `json:"projects,omitempty"`
}

// Datacenter as listed by GET /api/v1/dc.
type Datacenter struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec DatacenterSpec `json:"spec"`
}

type DatacenterSpec struct {
	Seed      string               `json:"seed"`
	Provider  string               `json:"provider"`
	Country   string               `json:"country,omitempty"`
	Location  string               `json:"location,omitempty"`
	OpenStack *DatacenterOpenStack `json:"openstack,omitempty"`
	AWS       *DatacenterRegion    `json:"aws,omitempty"`
	Azure     *DatacenterRegion    `json:"azure,omitempty"`
	GCP       *DatacenterGCP       `json:"gcp,omitempty"`
	Hetzner   *DatacenterHetzner   `json:"hetzner,omitempty"`
	DO        *DatacenterRegion    `json:"digitalocean,omitempty"`
}

// DatacenterRegion covers AWS (region, images), Azure (location, images) and DigitalOcean (region).
type DatacenterRegion struct {
	Region   string            `json:"region,omitempty"`
	Location string            `json:"location,omitempty"`
	Images   map[string]string `json:"images,omitempty"`
}

type DatacenterGCP struct {
	Region       string   `json:"region,omitempty"`
	ZoneSuffixes []string `json:"zoneSuffixes,omitempty"`
}

type DatacenterHetzner struct {
	Datacenter string `json:"datacenter,omitempty"`
	Location   string `json:"location,omitempty"`
	Network    string `json:"network,omitempty"`
}

type DatacenterOpenStack struct {
	AuthURL           string            `json:"authURL,omitempty"`
	Region            string            `json:"region,omitempty"`
	AvailabilityZone  string            `json:"availabilityZone,omitempty"`
	EnforceFloatingIP bool              `json:"enforceFloatingIP,omitempty"`
	Images            map[string]string `json:"images,omitempty"`
}

type MasterVersion struct {
	Version string `json:"version"`
	Default bool   `json:"default,omitempty"`
}

// Cluster health values.
const (
	HealthUp           = "HealthStatusUp"
	HealthDown         = "HealthStatusDown"
	HealthProvisioning = "HealthStatusProvisioning"
)

// ClusterHealth is GET …/clusters/{id}/health.
type ClusterHealth struct {
	Apiserver                    string `json:"apiserver,omitempty"`
	Scheduler                    string `json:"scheduler,omitempty"`
	Controller                   string `json:"controller,omitempty"`
	MachineController            string `json:"machineController,omitempty"`
	Etcd                         string `json:"etcd,omitempty"`
	CloudProviderInfrastructure  string `json:"cloudProviderInfrastructure,omitempty"`
	UserClusterControllerManager string `json:"userClusterControllerManager,omitempty"`
	OperatingSystemManager       string `json:"operatingSystemManager,omitempty"`
}

// Ready mirrors ExtendedClusterHealth.AllHealthy() upstream: the check KKP itself makes before it accepts
// machine deployments (it answers 503 until then).
func (h ClusterHealth) Ready() bool {
	up := func(s string) bool { return s == HealthUp }
	return up(h.Etcd) && up(h.Controller) && up(h.Apiserver) && up(h.Scheduler) &&
		(up(h.MachineController) || h.MachineController == "") &&
		up(h.CloudProviderInfrastructure) && up(h.UserClusterControllerManager)
}

type Cluster struct {
	ObjectMeta
	Type string `json:"type,omitempty"`
	// Credential names a preset on create.
	Credential string        `json:"credential,omitempty"`
	Spec       ClusterSpec   `json:"spec"`
	Status     ClusterStatus `json:"status,omitempty"`
}

type ClusterStatus struct {
	Version string `json:"version,omitempty"`
	URL     string `json:"url,omitempty"`
}

type NetworkRanges struct {
	CIDRBlocks []string `json:"cidrBlocks,omitempty"`
}

type ClusterSpec struct {
	Version                  string             `json:"version,omitempty"`
	Cloud                    CloudSpec          `json:"cloud"`
	ContainerRuntime         string             `json:"containerRuntime,omitempty"`
	CNIPlugin                *CNIPluginSettings `json:"cniPlugin,omitempty"`
	ExposeStrategy           string             `json:"exposeStrategy,omitempty"`
	APIServerAllowedIPRanges *NetworkRanges     `json:"apiServerAllowedIPRanges,omitempty"`
	ClusterNetwork           *ClusterNetwork    `json:"clusterNetwork,omitempty"`
	AuditLogging             *AuditLogging      `json:"auditLogging,omitempty"`
	AdmissionPlugins         []string           `json:"admissionPlugins,omitempty"`
	OPAIntegration           *Enabled           `json:"opaIntegration,omitempty"`
	Kyverno                  *Enabled           `json:"kyverno,omitempty"`
	KubernetesDashboard      *Enabled           `json:"kubernetesDashboard,omitempty"`
	MLA                      *MLASettings       `json:"mla,omitempty"`
	EnableUserSSHKeyAgent    *bool              `json:"enableUserSSHKeyAgent,omitempty"`
	DisableCSIDriver         bool               `json:"disableCsiDriver,omitempty"`
	UpdateWindow             *UpdateWindow      `json:"updateWindow,omitempty"`
	ComponentsOverride       map[string]any     `json:"componentsOverride,omitempty"`
	EncryptionConfiguration  *Enabled           `json:"encryptionConfiguration,omitempty"`
	Features                 map[string]bool    `json:"features,omitempty"`
	MachineNetworks          []map[string]any   `json:"machineNetworks,omitempty"`
	PodNodeSelectorConfig    map[string]string  `json:"podNodeSelectorAdmissionPluginConfig,omitempty"`
	ServiceAccount           map[string]any     `json:"serviceAccount,omitempty"`
	BackupConfig             map[string]any     `json:"backupConfig,omitempty"`
	KubeLB                   map[string]any     `json:"kubelb,omitempty"`
	EventRateLimitConfig     map[string]any     `json:"eventRateLimitConfig,omitempty"`
	OIDC                     map[string]any     `json:"oidc,omitempty"`
}

type Enabled struct {
	Enabled bool `json:"enabled"`
}

type MLASettings struct {
	MonitoringEnabled bool `json:"monitoringEnabled"`
	LoggingEnabled    bool `json:"loggingEnabled"`
}

type UpdateWindow struct {
	Start  string `json:"start,omitempty"`
	Length string `json:"length,omitempty"`
}

type AuditLogging struct {
	Enabled      bool   `json:"enabled"`
	PolicyPreset string `json:"policyPreset,omitempty"`
}

type CNIPluginSettings struct {
	Type    string `json:"type,omitempty"`
	Version string `json:"version,omitempty"`
}

type ClusterNetwork struct {
	IPFamily                 string         `json:"ipFamily,omitempty"`
	Pods                     *NetworkRanges `json:"pods,omitempty"`
	Services                 *NetworkRanges `json:"services,omitempty"`
	ProxyMode                string         `json:"proxyMode,omitempty"`
	NodeCIDRMaskSizeIPv4     *int64         `json:"nodeCidrMaskSizeIPv4,omitempty"`
	NodeLocalDNSCacheEnabled *bool          `json:"nodeLocalDNSCacheEnabled,omitempty"`
	KonnectivityEnabled      *bool          `json:"konnectivityEnabled,omitempty"`
	DNSDomain                string         `json:"dnsDomain,omitempty"`
}

// CloudSpec is spec.cloud: the datacenter plus one object keyed by the cloud provider (e.g. "openstack").
// The provider objects are kept as plain JSON maps; the provider package knows their fields. KKP never returns
// credential fields: it moves them into a secret on the seed and answers with credentialsReference instead.
type CloudSpec struct {
	DatacenterName string
	ProviderName   string
	Providers      map[string]map[string]any
}

func (c CloudSpec) MarshalJSON() ([]byte, error) {
	out := map[string]any{"dc": c.DatacenterName}
	if c.ProviderName != "" {
		out["providerName"] = c.ProviderName
	}
	for k, v := range c.Providers {
		out[k] = v
	}
	return json.Marshal(out)
}

func (c *CloudSpec) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*c = CloudSpec{Providers: map[string]map[string]any{}}
	for k, v := range raw {
		var err error
		switch k {
		case "dc":
			err = json.Unmarshal(v, &c.DatacenterName)
		case "providerName":
			err = json.Unmarshal(v, &c.ProviderName)
		default:
			var m map[string]any
			if json.Unmarshal(v, &m) == nil && m != nil {
				c.Providers[k] = m
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type CreateClusterBody struct {
	Cluster        Cluster         `json:"cluster"`
	NodeDeployment *NodeDeployment `json:"nodeDeployment,omitempty"`
}

type NodeDeployment struct {
	ObjectMeta
	Spec   NodeDeploymentSpec   `json:"spec"`
	Status NodeDeploymentStatus `json:"status,omitempty"`
}

type NodeDeploymentSpec struct {
	Replicas    int32    `json:"replicas"`
	MinReplicas *int32   `json:"minReplicas,omitempty"`
	MaxReplicas *int32   `json:"maxReplicas,omitempty"`
	Paused      *bool    `json:"paused,omitempty"`
	Template    NodeSpec `json:"template"`
}

type NodeDeploymentStatus struct {
	Replicas          int32 `json:"replicas,omitempty"`
	UpdatedReplicas   int32 `json:"updatedReplicas,omitempty"`
	ReadyReplicas     int32 `json:"readyReplicas,omitempty"`
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`
}

type NodeSpec struct {
	Cloud           NodeCloudSpec     `json:"cloud"`
	OperatingSystem OperatingSystem   `json:"operatingSystem"`
	Versions        NodeVersions      `json:"versions"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	Taints          []Taint           `json:"taints,omitempty"`
}

type NodeVersions struct {
	Kubelet string `json:"kubelet,omitempty"`
}

type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

// NodeCloudSpec is spec.template.cloud: one object keyed by the cloud provider, as plain JSON.
type NodeCloudSpec map[string]map[string]any

// OperatingSystem holds exactly one OS; the API rejects more than one.
type OperatingSystem struct {
	Ubuntu      *DistUpgrade `json:"ubuntu,omitempty"`
	Flatcar     *Flatcar     `json:"flatcar,omitempty"`
	RHEL        *DistUpgrade `json:"rhel,omitempty"`
	RockyLinux  *DistUpgrade `json:"rockylinux,omitempty"`
	AmazonLinux *DistUpgrade `json:"amzn2,omitempty"`
}

type DistUpgrade struct {
	DistUpgradeOnBoot bool `json:"distUpgradeOnBoot"`
}

type Flatcar struct {
	DisableAutoUpdate bool `json:"disableAutoUpdate"`
}

// ClusterServiceAccount is a Kubernetes service account in a user cluster; its ID is its name.
type ClusterServiceAccount struct {
	ObjectMeta
	Namespace string `json:"namespace,omitempty"`
}

// ClusterRoleUser is the subject of a cluster role binding: exactly one of a user, a group or a service account.
type ClusterRoleUser struct {
	UserEmail               string `json:"userEmail,omitempty"`
	Group                   string `json:"group,omitempty"`
	ServiceAccount          string `json:"serviceAccount,omitempty"`
	ServiceAccountNamespace string `json:"serviceAccountNamespace,omitempty"`
}

// Permission scopes.
const (
	PermissionCluster   = "Cluster"
	PermissionNamespace = "Namespace"
)

// Permission is one KKP binding of a cluster service account.
type Permission struct {
	Scope       string `json:"scope"`
	RoleRefName string `json:"roleRefName"`
	Namespace   string `json:"namespace"`
}
