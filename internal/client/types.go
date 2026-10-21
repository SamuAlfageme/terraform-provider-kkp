package client

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

type CloudSpec struct {
	DatacenterName string          `json:"dc"`
	ProviderName   string          `json:"providerName,omitempty"`
	OpenStack      *OpenStackCloud `json:"openstack,omitempty"`
}

// OpenStackCloud is spec.cloud.openstack. KKP never returns the credential fields: it moves them into a
// secret on the seed and answers with credentialsReference instead.
type OpenStackCloud struct {
	Username                    string            `json:"username,omitempty"`
	Password                    string            `json:"password,omitempty"`
	Domain                      string            `json:"domain,omitempty"`
	Project                     string            `json:"project,omitempty"`
	ProjectID                   string            `json:"projectID,omitempty"`
	ApplicationCredentialID     string            `json:"applicationCredentialID,omitempty"`
	ApplicationCredentialSecret string            `json:"applicationCredentialSecret,omitempty"`
	CredentialsReference        map[string]string `json:"credentialsReference,omitempty"`
	FloatingIPPool              string            `json:"floatingIPPool,omitempty"`
	Network                     string            `json:"network,omitempty"`
	SubnetID                    string            `json:"subnetID,omitempty"`
	RouterID                    string            `json:"routerID,omitempty"`
	SecurityGroups              string            `json:"securityGroups,omitempty"`
	NodePortsAllowedIPRanges    *NetworkRanges    `json:"nodePortsAllowedIPRanges,omitempty"`
	EnableIngressHostname       *bool             `json:"enableIngressHostname,omitempty"`
	IngressHostnameSuffix       string            `json:"ingressHostnameSuffix,omitempty"`
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

type NodeCloudSpec struct {
	OpenStack *OpenStackNode `json:"openstack,omitempty"`
}

type OpenStackNode struct {
	Flavor                    string            `json:"flavor"`
	Image                     string            `json:"image"`
	DiskSize                  *int64            `json:"diskSize,omitempty"`
	AvailabilityZone          string            `json:"availabilityZone,omitempty"`
	ServerGroup               string            `json:"serverGroup,omitempty"`
	UseFloatingIP             bool              `json:"useFloatingIP"`
	ConfigDrive               bool              `json:"configDrive,omitempty"`
	InstanceReadyCheckPeriod  string            `json:"instanceReadyCheckPeriod,omitempty"`
	InstanceReadyCheckTimeout string            `json:"instanceReadyCheckTimeout,omitempty"`
	Tags                      map[string]string `json:"tags,omitempty"`
}

// OperatingSystem holds exactly one OS; the API rejects more than one.
type OperatingSystem struct {
	Ubuntu     *DistUpgrade `json:"ubuntu,omitempty"`
	Flatcar    *Flatcar     `json:"flatcar,omitempty"`
	RHEL       *DistUpgrade `json:"rhel,omitempty"`
	RockyLinux *DistUpgrade `json:"rockylinux,omitempty"`
}

type DistUpgrade struct {
	DistUpgradeOnBoot bool `json:"distUpgradeOnBoot"`
}

type Flatcar struct {
	DisableAutoUpdate bool `json:"disableAutoUpdate"`
}
