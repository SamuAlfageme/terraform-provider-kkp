package provider

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
)

// kkp_project

func newProjectDataSource() datasource.DataSource { return &projectDataSource{} }

type projectDataSource struct{ dsBase }

type projectDSModel struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Labels types.Map    `tfsdk:"labels"`
	Status types.String `tfsdk:"status"`
	Owners types.List   `tfsdk:"owners"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a project by ID or name. With neither set, it returns the provider's default project " +
			"(for a service account token, the token's project).",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Optional: true, Computed: true},
			"name":   schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Must match exactly one project."},
			"labels": schema.MapAttribute{Computed: true, ElementType: types.StringType},
			"status": schema.StringAttribute{Computed: true},
			"owners": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Owner e-mail addresses."},
		},
	}
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m projectDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p *client.Project
	var err error
	switch {
	case m.ID.ValueString() != "":
		p, err = d.client().GetProject(ctx, m.ID.ValueString())
	case m.Name.ValueString() != "":
		var all []client.Project
		all, err = d.client().ListProjects(ctx)
		var hits []client.Project
		for _, x := range all {
			if x.Name == m.Name.ValueString() {
				hits = append(hits, x)
			}
		}
		if err == nil && len(hits) != 1 {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Project not found",
				fmt.Sprintf("%d projects visible to this token are named %q; expected exactly one.", len(hits), m.Name.ValueString()))
			return
		}
		if err == nil {
			p = &hits[0]
		}
	default:
		id := d.project(types.StringNull(), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		p, err = d.client().GetProject(ctx, id)
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read project", err)
		return
	}
	owners := make([]string, 0, len(p.Owners))
	for _, o := range p.Owners {
		owners = append(owners, o.Email)
	}
	m.ID, m.Name, m.Status = types.StringValue(p.ID), types.StringValue(p.Name), types.StringValue(p.Status)
	m.Labels = mapValue(p.Labels)
	m.Owners, _ = types.ListValueFrom(ctx, types.StringType, owners)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// kkp_projects

func newProjectsDataSource() datasource.DataSource { return &projectsDataSource{} }

type projectsDataSource struct{ dsBase }

type projectSummary struct {
	ID     types.String `tfsdk:"id"`
	Name   types.String `tfsdk:"name"`
	Status types.String `tfsdk:"status"`
	Labels types.Map    `tfsdk:"labels"`
}

type projectsDSModel struct {
	Projects []projectSummary `tfsdk:"projects"`
}

func (d *projectsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_projects"
}

func (d *projectsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the projects the token can see (a service account token sees only its own project).",
		Attributes: map[string]schema.Attribute{
			"projects": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true},
				"status": schema.StringAttribute{Computed: true}, "labels": schema.MapAttribute{Computed: true, ElementType: types.StringType},
			}}},
		},
	}
}

func (d *projectsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	all, err := d.client().ListProjects(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot list projects", err)
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	m := projectsDSModel{Projects: []projectSummary{}}
	for _, p := range all {
		m.Projects = append(m.Projects, projectSummary{types.StringValue(p.ID), types.StringValue(p.Name), types.StringValue(p.Status), mapValue(p.Labels)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// kkp_datacenter(s)

type datacenterModel struct {
	Name     types.String `tfsdk:"name"`
	Seed     types.String `tfsdk:"seed"`
	Provider types.String `tfsdk:"cloud_provider"`
	Country  types.String `tfsdk:"country"`
	Location types.String `tfsdk:"location"`
	// OpenStack only.
	AuthURL          types.String `tfsdk:"openstack_auth_url"`
	Region           types.String `tfsdk:"openstack_region"`
	AvailabilityZone types.String `tfsdk:"openstack_availability_zone"`
	EnforceFIP       types.Bool   `tfsdk:"openstack_enforce_floating_ip"`
	Images           types.Map    `tfsdk:"images"`
}

func datacenterAttributes(nameRequired bool) map[string]schema.Attribute {
	c := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	return map[string]schema.Attribute{
		"name":                          schema.StringAttribute{Required: nameRequired, Computed: !nameRequired},
		"seed":                          c("Seed cluster hosting the datacenter's control planes."),
		"cloud_provider":                c("Cloud provider, e.g. `openstack`."),
		"country":                       c("ISO country code."),
		"location":                      c("Human-readable location."),
		"openstack_auth_url":            c("Keystone URL (OpenStack)."),
		"openstack_region":              c("Region (OpenStack)."),
		"openstack_availability_zone":   c("Default availability zone (OpenStack)."),
		"openstack_enforce_floating_ip": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether nodes must get floating IPs (OpenStack)."},
		"images":                        schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Default image per operating system."},
	}
}

func toDatacenterModel(dc client.Datacenter) datacenterModel {
	m := datacenterModel{
		Name: types.StringValue(dc.Metadata.Name), Seed: types.StringValue(dc.Spec.Seed), Provider: types.StringValue(dc.Spec.Provider),
		Country: strOrNull(dc.Spec.Country), Location: strOrNull(dc.Spec.Location),
		AuthURL: types.StringNull(), Region: types.StringNull(), AvailabilityZone: types.StringNull(), EnforceFIP: types.BoolNull(),
		Images: types.MapNull(types.StringType),
	}
	if os := dc.Spec.OpenStack; os != nil {
		m.AuthURL, m.Region, m.AvailabilityZone = strOrNull(os.AuthURL), strOrNull(os.Region), strOrNull(os.AvailabilityZone)
		m.EnforceFIP = types.BoolValue(os.EnforceFloatingIP)
		m.Images = mapValue(os.Images)
	}
	return m
}

func newDatacenterDataSource() datasource.DataSource { return &datacenterDataSource{} }

type datacenterDataSource struct{ dsBase }

func (d *datacenterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_datacenter"
}

func (d *datacenterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "One datacenter, by name.", Attributes: datacenterAttributes(true)}
}

func (d *datacenterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m datacenterModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	dc, err := d.client().GetDatacenter(ctx, m.Name.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read datacenter", err)
		return
	}
	out := toDatacenterModel(*dc)
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

func newDatacentersDataSource() datasource.DataSource { return &datacentersDataSource{} }

type datacentersDataSource struct{ dsBase }

type datacentersModel struct {
	Provider    types.String      `tfsdk:"cloud_provider"`
	Seed        types.String      `tfsdk:"seed"`
	Names       types.List        `tfsdk:"names"`
	Datacenters []datacenterModel `tfsdk:"datacenters"`
}

func (d *datacentersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_datacenters"
}

func (d *datacentersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the datacenters clusters can be created in.",
		Attributes: map[string]schema.Attribute{
			"cloud_provider": schema.StringAttribute{Optional: true, MarkdownDescription: "Only datacenters of this cloud provider, e.g. `openstack`."},
			"seed":           schema.StringAttribute{Optional: true, MarkdownDescription: "Only datacenters of this seed."},
			"names":          schema.ListAttribute{Computed: true, ElementType: types.StringType},
			"datacenters":    schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: datacenterAttributes(false)}},
		},
	}
}

func (d *datacentersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m datacentersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	all, err := d.client().ListDatacenters(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot list datacenters", err)
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Metadata.Name < all[j].Metadata.Name })
	m.Datacenters = []datacenterModel{}
	names := []string{}
	for _, dc := range all {
		if p := m.Provider.ValueString(); p != "" && dc.Spec.Provider != p {
			continue
		}
		if s := m.Seed.ValueString(); s != "" && dc.Spec.Seed != s {
			continue
		}
		m.Datacenters = append(m.Datacenters, toDatacenterModel(dc))
		names = append(names, dc.Metadata.Name)
	}
	m.Names, _ = types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// kkp_kubernetes_versions

func newKubernetesVersionsDataSource() datasource.DataSource { return &versionsDataSource{} }

type versionsDataSource struct{ dsBase }

type versionsModel struct {
	Provider       types.String `tfsdk:"cloud_provider"`
	Versions       types.List   `tfsdk:"versions"`
	DefaultVersion types.String `tfsdk:"default_version"`
	LatestVersion  types.String `tfsdk:"latest_version"`
}

func (d *versionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_versions"
}

func (d *versionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Kubernetes versions KKP offers for new clusters of a provider.",
		Attributes: map[string]schema.Attribute{
			"cloud_provider": schema.StringAttribute{Optional: true, MarkdownDescription: "Cloud provider; defaults to `openstack`.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"versions":        schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Oldest first."},
			"default_version": schema.StringAttribute{Computed: true, MarkdownDescription: "The version KKP preselects."},
			"latest_version":  schema.StringAttribute{Computed: true},
		},
	}
}

func (d *versionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m versionsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	provider := m.Provider.ValueString()
	if provider == "" {
		provider = "openstack"
	}
	vs, err := d.client().ListVersions(ctx, provider)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot list versions", err)
		return
	}
	var list []string
	def := ""
	for _, v := range vs {
		list = append(list, v.Version)
		if v.Default {
			def = v.Version
		}
	}
	slices.SortFunc(list, compareVersions)
	m.Versions, _ = types.ListValueFrom(ctx, types.StringType, list)
	m.DefaultVersion = strOrNull(def)
	m.LatestVersion = types.StringNull()
	if len(list) > 0 {
		m.LatestVersion = types.StringValue(list[len(list)-1])
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func compareVersions(a, b string) int {
	var x, y [3]int
	_, _ = fmt.Sscanf(a, "%d.%d.%d", &x[0], &x[1], &x[2])
	_, _ = fmt.Sscanf(b, "%d.%d.%d", &y[0], &y[1], &y[2])
	return slices.Compare(x[:], y[:])
}

// kkp_cluster

func newClusterDataSource() datasource.DataSource { return &clusterDataSource{} }

type clusterDataSource struct{ dsBase }

type clusterDSModel struct {
	ID         types.String `tfsdk:"id"`
	ProjectID  types.String `tfsdk:"project_id"`
	Name       types.String `tfsdk:"name"`
	Datacenter types.String `tfsdk:"datacenter"`
	Provider   types.String `tfsdk:"cloud_provider"`
	Version    types.String `tfsdk:"version"`
	Labels     types.Map    `tfsdk:"labels"`
	URL        types.String `tfsdk:"url"`
	Ready      types.Bool   `tfsdk:"ready"`
	Upgrades   types.List   `tfsdk:"available_upgrades"`
}

func (d *clusterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster"
}

func (d *clusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	c := func() schema.StringAttribute { return schema.StringAttribute{Computed: true} }
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a cluster by ID or by name.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Optional: true, Computed: true},
			"project_id":         schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Defaults to the provider's `project_id`."},
			"name":               schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Must match exactly one cluster of the project."},
			"datacenter":         c(),
			"cloud_provider":     c(),
			"version":            c(),
			"labels":             schema.MapAttribute{Computed: true, ElementType: types.StringType},
			"url":                c(),
			"ready":              schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether all control plane components are healthy."},
			"available_upgrades": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Versions the control plane can be upgraded to."},
		},
	}
}

func (d *clusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m clusterDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	project := d.project(m.ProjectID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.ID.ValueString() == "" && m.Name.ValueString() == "" {
		resp.Diagnostics.AddError("Missing cluster", "Set id or name.")
		return
	}
	var c *client.Cluster
	var err error
	if id := m.ID.ValueString(); id != "" {
		c, err = d.client().GetCluster(ctx, project, id)
	} else {
		var all []client.Cluster
		all, err = d.client().ListClusters(ctx, project)
		var hits []client.Cluster
		for _, x := range all {
			if x.Name == m.Name.ValueString() {
				hits = append(hits, x)
			}
		}
		if err == nil && len(hits) != 1 {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Cluster not found",
				fmt.Sprintf("%d clusters in project %s are named %q; expected exactly one.", len(hits), project, m.Name.ValueString()))
			return
		}
		if err == nil {
			c = &hits[0]
		}
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster", err)
		return
	}
	h, err := d.client().GetClusterHealth(ctx, project, c.ID)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster health", err)
		return
	}
	ups, err := d.client().ClusterUpgrades(ctx, project, c.ID)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster upgrades", err)
		return
	}
	upgrades := []string{}
	for _, u := range ups {
		upgrades = append(upgrades, u.Version)
	}
	slices.SortFunc(upgrades, compareVersions)
	m.ID, m.ProjectID, m.Name = types.StringValue(c.ID), types.StringValue(project), types.StringValue(c.Name)
	m.Datacenter, m.Version = types.StringValue(c.Spec.Cloud.DatacenterName), types.StringValue(c.Spec.Version)
	m.Provider = strOrNull(c.Spec.Cloud.ProviderName)
	m.Labels = mapValue(withoutKeys(c.Labels, systemClusterLabels...))
	m.URL = strOrNull(c.Status.URL)
	m.Ready = types.BoolValue(h.Ready())
	m.Upgrades, _ = types.ListValueFrom(ctx, types.StringType, upgrades)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// kkp_cluster_kubeconfig

func newClusterKubeconfigDataSource() datasource.DataSource { return &kubeconfigDataSource{} }

type kubeconfigDataSource struct{ dsBase }

type kubeconfigModel struct {
	ProjectID  types.String `tfsdk:"project_id"`
	ClusterID  types.String `tfsdk:"cluster_id"`
	Kubeconfig types.String `tfsdk:"kubeconfig"`
}

func (d *kubeconfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_kubeconfig"
}

func (d *kubeconfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The admin kubeconfig of a cluster, for the `kubernetes` and `helm` providers. It ends up in state, " +
			"so treat the state as a secret. Needs an editor or owner token.",
		Attributes: map[string]schema.Attribute{
			"project_id": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Defaults to the provider's `project_id`."},
			"cluster_id": schema.StringAttribute{Required: true},
			"kubeconfig": schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Kubeconfig YAML."},
		},
	}
}

func (d *kubeconfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m kubeconfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	project := d.project(m.ProjectID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	kc, err := d.client().GetAdminKubeconfig(ctx, project, m.ClusterID.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot get kubeconfig", err)
		return
	}
	m.ProjectID = types.StringValue(project)
	m.Kubeconfig = types.StringValue(kc)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
