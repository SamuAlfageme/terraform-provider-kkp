package provider

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
)

var (
	_ resource.ResourceWithImportState    = (*clusterResource)(nil)
	_ resource.ResourceWithValidateConfig = (*clusterResource)(nil)
)

// Labels KKP manages on clusters; they never show up in, or get removed from, the labels attribute.
var systemClusterLabels = []string{"project-id", "is-credential-preset", "worker-name"}

func newClusterResource() resource.Resource { return &clusterResource{} }

type clusterResource struct{ base }

type clusterModel struct {
	ID                       types.String   `tfsdk:"id"`
	ProjectID                types.String   `tfsdk:"project_id"`
	Name                     types.String   `tfsdk:"name"`
	Datacenter               types.String   `tfsdk:"datacenter"`
	Version                  types.String   `tfsdk:"version"`
	CredentialPreset         types.String   `tfsdk:"credential_preset"`
	Labels                   types.Map      `tfsdk:"labels"`
	ExposeStrategy           types.String   `tfsdk:"expose_strategy"`
	CNIType                  types.String   `tfsdk:"cni_type"`
	CNIVersion               types.String   `tfsdk:"cni_version"`
	PodsCIDR                 types.String   `tfsdk:"pods_cidr"`
	ServicesCIDR             types.String   `tfsdk:"services_cidr"`
	ProxyMode                types.String   `tfsdk:"proxy_mode"`
	NodeLocalDNSCache        types.Bool     `tfsdk:"node_local_dns_cache"`
	Konnectivity             types.Bool     `tfsdk:"konnectivity"`
	APIServerAllowedIPRanges types.List     `tfsdk:"api_server_allowed_ip_ranges"`
	AuditLogging             types.Bool     `tfsdk:"audit_logging"`
	AuditPolicyPreset        types.String   `tfsdk:"audit_policy_preset"`
	AdmissionPlugins         types.Set      `tfsdk:"admission_plugins"`
	OPAIntegration           types.Bool     `tfsdk:"opa_integration"`
	Kyverno                  types.Bool     `tfsdk:"kyverno"`
	KubernetesDashboard      types.Bool     `tfsdk:"kubernetes_dashboard"`
	MLALogging               types.Bool     `tfsdk:"mla_logging"`
	MLAMonitoring            types.Bool     `tfsdk:"mla_monitoring"`
	UserSSHKeyAgent          types.Bool     `tfsdk:"user_ssh_key_agent"`
	SSHKeyIDs                types.Set      `tfsdk:"ssh_key_ids"`
	OpenStack                types.Object   `tfsdk:"openstack"`
	AWS                      types.Object   `tfsdk:"aws"`
	Azure                    types.Object   `tfsdk:"azure"`
	GCP                      types.Object   `tfsdk:"gcp"`
	Hetzner                  types.Object   `tfsdk:"hetzner"`
	DigitalOcean             types.Object   `tfsdk:"digitalocean"`
	DeleteVolumes            types.Bool     `tfsdk:"delete_volumes_on_destroy"`
	DeleteLoadBalancers      types.Bool     `tfsdk:"delete_load_balancers_on_destroy"`
	WaitForReady             types.Bool     `tfsdk:"wait_for_ready"`
	URL                      types.String   `tfsdk:"url"`
	CreationTimestamp        types.String   `tfsdk:"creation_timestamp"`
	Timeouts                 timeouts.Value `tfsdk:"timeouts"`
}

// clouds maps the cloud block names to the model's fields.
func (m *clusterModel) clouds() map[string]*types.Object {
	return map[string]*types.Object{"openstack": &m.OpenStack, "aws": &m.AWS, "azure": &m.Azure, "gcp": &m.GCP,
		"hetzner": &m.Hetzner, "digitalocean": &m.DigitalOcean}
}

func cloudValues(blocks map[string]*types.Object) map[string]types.Object {
	out := map[string]types.Object{}
	for k, v := range blocks {
		out[k] = *v
	}
	return out
}

// activeCloud is the cloud block that is set, if any.
func activeCloud(defs []cloudDef, blocks map[string]*types.Object) (cloudDef, bool) {
	for _, d := range defs {
		if o := *blocks[d.Name]; !o.IsNull() && !o.IsUnknown() {
			return d, true
		}
	}
	return cloudDef{}, false
}

func (r *clusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster"
}

// computedString is Optional+Computed and keeps its state value when the config leaves it out.
func computedString(desc string, replace bool, validators ...validator.String) schema.StringAttribute {
	mods := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	if replace {
		mods = append(mods, stringplanmodifier.RequiresReplace())
	}
	return schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: desc, PlanModifiers: mods, Validators: validators}
}

func computedBool(desc string, replace bool) schema.BoolAttribute {
	mods := []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}
	if replace {
		mods = append(mods, boolplanmodifier.RequiresReplace())
	}
	return schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: desc, PlanModifiers: mods}
}

func defaultBool(desc string, def bool) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: desc, Default: booldefault.StaticBool(def)}
}

func (r *clusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{}
	for _, d := range clusterClouds {
		attrs[d.Name] = d.schema("cluster settings")
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A KKP user cluster on OpenStack, AWS, Azure, Google Cloud, Hetzner or DigitalOcean: set the block of the " +
			"datacenter's cloud provider. Worker nodes are managed separately with `kkp_machine_deployment`.\n\nKKP only accepts in-place changes to some fields (name, labels, version, " +
			"expose strategy, CNI version, proxy mode, Konnectivity, allowed IP ranges, audit logging, admission plugins, " +
			"OPA, Kyverno, MLA, the Kubernetes dashboard and cloud credentials); the provider replaces the cluster when " +
			"anything else changes. Upgrade the control plane one minor version at a time, and keep machine deployments " +
			"within KKP's kubelet version skew.",
		Attributes: map[string]schema.Attribute{
			"id":                idAttribute("Cluster ID, generated by KKP."),
			"project_id":        projectIDAttribute(),
			"name":              schema.StringAttribute{Required: true, MarkdownDescription: "Display name."},
			"datacenter":        replaceString(true, "Datacenter name, see the `kkp_datacenters` data source."),
			"version":           schema.StringAttribute{Required: true, MarkdownDescription: "Kubernetes version of the control plane, e.g. `1.32.5`. See `kkp_kubernetes_versions`."},
			"credential_preset": replaceString(false, "Name of a KKP preset to take the cloud credentials from, instead of setting them in the cloud block."),
			"labels": schema.MapAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Labels. Labels KKP manages itself (`project-id`, `is-credential-preset`, `worker-name`) are ignored."},
			"expose_strategy": computedString("How the API server is exposed: `NodePort`, `LoadBalancer` or `Tunneling`. Defaults to the seed's setting.", false,
				stringvalidator.OneOf("NodePort", "LoadBalancer", "Tunneling")),
			"cni_type":      computedString("CNI plugin: `cilium`, `canal` or `none`. Defaults to KKP's choice (Cilium).", true, stringvalidator.OneOf("cilium", "canal", "none")),
			"cni_version":   computedString("CNI plugin version. Defaults to KKP's default for the plugin; can be upgraded in place.", false),
			"pods_cidr":     computedString("IPv4 pod network. Defaults to KKP's (172.25.0.0/16).", true),
			"services_cidr": computedString("IPv4 service network. Defaults to KKP's (10.240.16.0/20).", true),
			"proxy_mode": computedString("kube-proxy mode: `ipvs`, `iptables`, `ebpf` (Cilium only) or `nftables`.", false,
				stringvalidator.OneOf("ipvs", "iptables", "ebpf", "nftables")),
			"node_local_dns_cache": computedBool("Run NodeLocal DNSCache on the nodes.", true),
			"konnectivity":         computedBool("Use Konnectivity for control plane to node traffic.", false),
			"api_server_allowed_ip_ranges": schema.ListAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "CIDRs allowed to reach the API server; only enforced with the `LoadBalancer` expose strategy."},
			"audit_logging":        computedBool("Enable Kubernetes audit logging.", false),
			"audit_policy_preset":  schema.StringAttribute{Optional: true, MarkdownDescription: "Audit policy preset: `metadata`, `recommended` or `minimal`.", Validators: []validator.String{stringvalidator.OneOf("metadata", "recommended", "minimal")}},
			"admission_plugins":    schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Extra admission plugins, e.g. `PodNodeSelector`."},
			"opa_integration":      computedBool("Enable the OPA Gatekeeper integration.", false),
			"kyverno":              computedBool("Enable Kyverno (Enterprise Edition).", false),
			"kubernetes_dashboard": computedBool("Deploy the Kubernetes dashboard.", false),
			"mla_logging":          computedBool("Enable user cluster logging (MLA).", false),
			"mla_monitoring":       computedBool("Enable user cluster monitoring (MLA).", false),
			"user_ssh_key_agent":   computedBool("Run KKP's user SSH key agent, which syncs `ssh_key_ids` to the nodes. Cannot be changed after creation.", true),
			"ssh_key_ids": schema.SetAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "IDs of project SSH keys (`kkp_ssh_key.id`) to put on the nodes."},
			"delete_volumes_on_destroy":        defaultBool("Delete the cluster's persistent volumes when the cluster is destroyed.", false),
			"delete_load_balancers_on_destroy": defaultBool("Delete the cluster's load balancers when the cluster is destroyed.", false),
			"wait_for_ready":                   defaultBool("Wait until the control plane is healthy after create and upgrade.", true),
			"url":                              idAttribute("API server URL."),
			"creation_timestamp":               idAttribute(""),
			"timeouts":                         timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
	maps.Copy(resp.Schema.Attributes, attrs)
}

func (r *clusterResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg clusterModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, ok := exactlyOneCloud(clusterClouds, cloudValues(cfg.clouds()), &resp.Diagnostics)
	if !ok || cfg.CredentialPreset.IsUnknown() {
		return
	}
	d.validateCredentials(*cfg.clouds()[d.Name], cfg.CredentialPreset.ValueString() != "", &resp.Diagnostics)
}

func boolOrNil(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return ptr(v.ValueBool())
}

func known[T interface {
	IsNull() bool
	IsUnknown() bool
}](v T) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func enabled(v types.Bool) *client.Enabled {
	if !known(v) {
		return nil
	}
	return &client.Enabled{Enabled: v.ValueBool()}
}

func ranges(ctx context.Context, l types.List, diags *diagsT) *client.NetworkRanges {
	s := goStrings(ctx, l, diags)
	if len(s) == 0 {
		return nil
	}
	return &client.NetworkRanges{CIDRBlocks: s}
}

func (r *clusterResource) build(ctx context.Context, m *clusterModel, diags *diagsT) client.CreateClusterBody {
	cloud := client.CloudSpec{DatacenterName: m.Datacenter.ValueString(), Providers: map[string]map[string]any{}}
	if d, ok := activeCloud(clusterClouds, m.clouds()); ok {
		cloud.Providers[d.Name] = d.body(ctx, *m.clouds()[d.Name])
	}
	spec := client.ClusterSpec{
		Version:                  m.Version.ValueString(),
		Cloud:                    cloud,
		ExposeStrategy:           m.ExposeStrategy.ValueString(),
		APIServerAllowedIPRanges: ranges(ctx, m.APIServerAllowedIPRanges, diags),
		AdmissionPlugins:         goStrings(ctx, m.AdmissionPlugins, diags),
		OPAIntegration:           enabled(m.OPAIntegration),
		Kyverno:                  enabled(m.Kyverno),
		KubernetesDashboard:      enabled(m.KubernetesDashboard),
		EnableUserSSHKeyAgent:    boolOrNil(m.UserSSHKeyAgent),
	}
	if known(m.CNIType) || known(m.CNIVersion) {
		spec.CNIPlugin = &client.CNIPluginSettings{Type: m.CNIType.ValueString(), Version: m.CNIVersion.ValueString()}
	}
	nw := client.ClusterNetwork{ProxyMode: m.ProxyMode.ValueString(), NodeLocalDNSCacheEnabled: boolOrNil(m.NodeLocalDNSCache), KonnectivityEnabled: boolOrNil(m.Konnectivity)}
	if v := m.PodsCIDR.ValueString(); v != "" {
		nw.Pods = &client.NetworkRanges{CIDRBlocks: []string{v}}
	}
	if v := m.ServicesCIDR.ValueString(); v != "" {
		nw.Services = &client.NetworkRanges{CIDRBlocks: []string{v}}
	}
	if nw != (client.ClusterNetwork{}) {
		spec.ClusterNetwork = &nw
	}
	if known(m.AuditLogging) || known(m.AuditPolicyPreset) {
		spec.AuditLogging = &client.AuditLogging{Enabled: m.AuditLogging.ValueBool(), PolicyPreset: m.AuditPolicyPreset.ValueString()}
	}
	if known(m.MLALogging) || known(m.MLAMonitoring) {
		spec.MLA = &client.MLASettings{LoggingEnabled: m.MLALogging.ValueBool(), MonitoringEnabled: m.MLAMonitoring.ValueBool()}
	}
	return client.CreateClusterBody{Cluster: client.Cluster{
		ObjectMeta: client.ObjectMeta{Name: m.Name.ValueString(), Labels: goMap(ctx, m.Labels, diags)},
		Type:       "kubernetes",
		Credential: m.CredentialPreset.ValueString(),
		Spec:       spec,
	}}
}

// fill copies what KKP reports into m. Credentials and provider-only settings keep their values from m.
func (r *clusterResource) fill(m *clusterModel, projectID string, c *client.Cluster, sshKeys []string) {
	s := c.Spec
	m.ID = types.StringValue(c.ID)
	m.ProjectID = types.StringValue(projectID)
	m.Name = types.StringValue(c.Name)
	m.Datacenter = types.StringValue(s.Cloud.DatacenterName)
	m.Version = types.StringValue(s.Version)
	m.Labels = mapKeep(withoutKeys(c.Labels, systemClusterLabels...), m.Labels)
	m.ExposeStrategy = strOrNull(s.ExposeStrategy)
	cni := s.CNIPlugin
	if cni == nil {
		cni = &client.CNIPluginSettings{}
	}
	m.CNIType, m.CNIVersion = strOrNull(cni.Type), strOrNull(cni.Version)
	nw := s.ClusterNetwork
	if nw == nil {
		nw = &client.ClusterNetwork{}
	}
	first := func(r *client.NetworkRanges) string {
		if r == nil || len(r.CIDRBlocks) == 0 {
			return ""
		}
		return r.CIDRBlocks[0]
	}
	m.PodsCIDR, m.ServicesCIDR = strOrNull(first(nw.Pods)), strOrNull(first(nw.Services))
	m.ProxyMode = strOrNull(nw.ProxyMode)
	m.NodeLocalDNSCache = types.BoolValue(nw.NodeLocalDNSCacheEnabled != nil && *nw.NodeLocalDNSCacheEnabled)
	m.Konnectivity = types.BoolValue(nw.KonnectivityEnabled != nil && *nw.KonnectivityEnabled)
	cidrs := func(r *client.NetworkRanges) []string {
		if r == nil {
			return nil
		}
		return r.CIDRBlocks
	}
	m.APIServerAllowedIPRanges = listValue(cidrs(s.APIServerAllowedIPRanges), m.APIServerAllowedIPRanges)
	audit := s.AuditLogging
	if audit == nil {
		audit = &client.AuditLogging{}
	}
	m.AuditLogging = types.BoolValue(audit.Enabled)
	m.AuditPolicyPreset = strOrNull(audit.PolicyPreset)
	m.AdmissionPlugins = setValue(s.AdmissionPlugins, m.AdmissionPlugins)
	on := func(e *client.Enabled) types.Bool { return types.BoolValue(e != nil && e.Enabled) }
	m.OPAIntegration, m.Kyverno, m.KubernetesDashboard = on(s.OPAIntegration), on(s.Kyverno), on(s.KubernetesDashboard)
	mla := s.MLA
	if mla == nil {
		mla = &client.MLASettings{}
	}
	m.MLALogging, m.MLAMonitoring = types.BoolValue(mla.LoggingEnabled), types.BoolValue(mla.MonitoringEnabled)
	m.UserSSHKeyAgent = types.BoolValue(s.EnableUserSSHKeyAgent != nil && *s.EnableUserSSHKeyAgent)
	m.SSHKeyIDs = setValue(sshKeys, m.SSHKeyIDs)

	for _, d := range clusterClouds {
		o := m.clouds()[d.Name]
		if raw, ok := s.Cloud.Providers[d.Name]; ok {
			*o = d.fromJSON(raw, *o)
		} else {
			*o = d.null()
		}
	}
	m.URL = strOrNull(c.Status.URL)
	m.CreationTimestamp = types.StringValue(c.CreationTimestamp)
	for _, b := range []*types.Bool{&m.DeleteVolumes, &m.DeleteLoadBalancers} {
		if b.IsNull() || b.IsUnknown() {
			*b = types.BoolValue(false)
		}
	}
	if m.WaitForReady.IsNull() || m.WaitForReady.IsUnknown() {
		m.WaitForReady = types.BoolValue(true)
	}
}

func (r *clusterResource) refresh(ctx context.Context, m *clusterModel, projectID, id string) error {
	c, err := r.client().GetCluster(ctx, projectID, id)
	if err != nil {
		return err
	}
	keys, err := r.client().ListClusterSSHKeys(ctx, projectID, id)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	r.fill(m, projectID, c, ids)
	return nil
}

func (r *clusterResource) waitReady(ctx context.Context, projectID, id string) error {
	return waitFor(ctx, "cluster control plane to become healthy", func() (bool, error) {
		h, err := r.client().GetClusterHealth(ctx, projectID, id)
		if err != nil {
			return false, err
		}
		return h.Ready(), nil
	})
}

func (r *clusterResource) syncSSHKeys(ctx context.Context, projectID, id string, old, want []string) error {
	for _, k := range want {
		if !slices.Contains(old, k) {
			if err := r.client().AssignClusterSSHKey(ctx, projectID, id, k); err != nil {
				return err
			}
		}
	}
	for _, k := range old {
		if !slices.Contains(want, k) {
			if err := r.client().DetachClusterSSHKey(ctx, projectID, id, k); err != nil && !client.IsNotFound(err) {
				return err
			}
		}
	}
	return nil
}

func (r *clusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	project := r.project(plan.ProjectID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, 30*time.Minute)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body := r.build(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	wantKeys := goStrings(ctx, plan.SSHKeyIDs, &resp.Diagnostics)
	c, err := r.client().CreateCluster(ctx, project, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create cluster", err)
		return
	}
	// Record the cluster before waiting, so a failure later leaves it tainted instead of orphaned.
	state := plan
	r.fill(&state, project, c, nil)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

	if err := r.syncSSHKeys(ctx, project, c.ID, nil, wantKeys); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot assign SSH keys to the cluster", err)
		return
	}
	if plan.WaitForReady.ValueBool() {
		if err := r.waitReady(ctx, project, c.ID); err != nil {
			addAPIError(&resp.Diagnostics, "Cluster did not become ready", err)
			return
		}
	}
	if err := r.refresh(ctx, &plan, project, c.ID); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *clusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.refresh(ctx, &state, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func changedString(plan, state types.String) bool { return known(plan) && !plan.Equal(state) }
func changedBool(plan, state types.Bool) bool     { return known(plan) && !plan.Equal(state) }

func cidrPatch(ctx context.Context, l types.List, diags *diagsT) any {
	if s := goStrings(ctx, l, diags); len(s) > 0 {
		return map[string]any{"cidrBlocks": s}
	}
	return nil
}

func (r *clusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state clusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Update(ctx, 30*time.Minute)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	project, id := state.ProjectID.ValueString(), state.ID.ValueString()
	diags := &resp.Diagnostics

	patch := map[string]any{}
	spec := map[string]any{}
	if changedString(plan.Name, state.Name) {
		patch["name"] = plan.Name.ValueString()
	}
	if lp := mapPatch(goMap(ctx, state.Labels, diags), goMap(ctx, plan.Labels, diags)); len(lp) > 0 {
		patch["labels"] = lp
	}
	upgraded := changedString(plan.Version, state.Version)
	if upgraded {
		spec["version"] = plan.Version.ValueString()
	}
	if changedString(plan.ExposeStrategy, state.ExposeStrategy) {
		spec["exposeStrategy"] = plan.ExposeStrategy.ValueString()
	}
	if changedString(plan.CNIVersion, state.CNIVersion) {
		spec["cniPlugin"] = map[string]any{"version": plan.CNIVersion.ValueString()}
	}
	nw := map[string]any{}
	if changedString(plan.ProxyMode, state.ProxyMode) {
		nw["proxyMode"] = plan.ProxyMode.ValueString()
	}
	if changedBool(plan.Konnectivity, state.Konnectivity) {
		nw["konnectivityEnabled"] = plan.Konnectivity.ValueBool()
	}
	if len(nw) > 0 {
		spec["clusterNetwork"] = nw
	}
	if !plan.APIServerAllowedIPRanges.Equal(state.APIServerAllowedIPRanges) {
		spec["apiServerAllowedIPRanges"] = cidrPatch(ctx, plan.APIServerAllowedIPRanges, diags)
	}
	if changedBool(plan.AuditLogging, state.AuditLogging) || !plan.AuditPolicyPreset.Equal(state.AuditPolicyPreset) {
		a := map[string]any{"enabled": plan.AuditLogging.ValueBool(), "policyPreset": nil}
		if v := plan.AuditPolicyPreset.ValueString(); v != "" {
			a["policyPreset"] = v
		}
		spec["auditLogging"] = a
	}
	if !plan.AdmissionPlugins.Equal(state.AdmissionPlugins) {
		if p := goStrings(ctx, plan.AdmissionPlugins, diags); len(p) > 0 {
			spec["admissionPlugins"] = p
		} else {
			spec["admissionPlugins"] = nil
		}
	}
	for key, v := range map[string][2]types.Bool{
		"opaIntegration":      {plan.OPAIntegration, state.OPAIntegration},
		"kyverno":             {plan.Kyverno, state.Kyverno},
		"kubernetesDashboard": {plan.KubernetesDashboard, state.KubernetesDashboard},
	} {
		if changedBool(v[0], v[1]) {
			spec[key] = map[string]any{"enabled": v[0].ValueBool()}
		}
	}
	if changedBool(plan.MLALogging, state.MLALogging) || changedBool(plan.MLAMonitoring, state.MLAMonitoring) {
		spec["mla"] = map[string]any{"loggingEnabled": plan.MLALogging.ValueBool(), "monitoringEnabled": plan.MLAMonitoring.ValueBool()}
	}
	if d, ok := activeCloud(clusterClouds, plan.clouds()); ok {
		if cp := d.patch(ctx, *plan.clouds()[d.Name], *state.clouds()[d.Name], false); len(cp) > 0 {
			spec["cloud"] = map[string]any{d.Name: cp}
		}
	}
	if len(spec) > 0 {
		patch["spec"] = spec
	}
	if diags.HasError() {
		return
	}

	if len(patch) > 0 {
		if _, err := r.client().PatchCluster(ctx, project, id, patch); err != nil {
			addAPIError(diags, "Cannot update cluster", err)
			return
		}
	}
	if !plan.SSHKeyIDs.Equal(state.SSHKeyIDs) {
		err := r.syncSSHKeys(ctx, project, id, goStrings(ctx, state.SSHKeyIDs, diags), goStrings(ctx, plan.SSHKeyIDs, diags))
		if err != nil {
			addAPIError(diags, "Cannot update the cluster's SSH keys", err)
			return
		}
	}
	if upgraded && plan.WaitForReady.ValueBool() {
		if err := r.waitReady(ctx, project, id); err != nil {
			addAPIError(diags, "Cluster did not become ready after the upgrade", err)
			return
		}
	}
	if err := r.refresh(ctx, &plan, project, id); err != nil {
		addAPIError(diags, "Cannot read cluster", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *clusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, 30*time.Minute)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	project, id := state.ProjectID.ValueString(), state.ID.ValueString()

	err := r.client().DeleteCluster(ctx, project, id, state.DeleteVolumes.ValueBool(), state.DeleteLoadBalancers.ValueBool())
	if err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Cannot delete cluster", err)
		return
	}
	if err := waitGone(ctx, "cluster", func() error { _, err := r.client().GetCluster(ctx, project, id); return err }); err != nil {
		addAPIError(&resp.Diagnostics, "Cluster was not deleted", err)
	}
}

func (r *clusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 2, "<project_id>/<cluster_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
