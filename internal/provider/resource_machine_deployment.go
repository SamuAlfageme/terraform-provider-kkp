package provider

import (
	"context"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
)

var (
	_ resource.ResourceWithImportState    = (*machineDeploymentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*machineDeploymentResource)(nil)
)

// ospAnnotation picks the operating system profile of a machine deployment.
const ospAnnotation = "k8c.io/operating-system-profile"

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func newMachineDeploymentResource() resource.Resource { return &machineDeploymentResource{} }

type machineDeploymentResource struct{ base }

type machineDeploymentModel struct {
	ID                       types.String   `tfsdk:"id"`
	ProjectID                types.String   `tfsdk:"project_id"`
	ClusterID                types.String   `tfsdk:"cluster_id"`
	Name                     types.String   `tfsdk:"name"`
	Replicas                 types.Int64    `tfsdk:"replicas"`
	MinReplicas              types.Int64    `tfsdk:"min_replicas"`
	MaxReplicas              types.Int64    `tfsdk:"max_replicas"`
	Paused                   types.Bool     `tfsdk:"paused"`
	KubeletVersion           types.String   `tfsdk:"kubelet_version"`
	OperatingSystem          types.String   `tfsdk:"operating_system"`
	DistUpgradeOnBoot        types.Bool     `tfsdk:"dist_upgrade_on_boot"`
	FlatcarDisableAutoUpdate types.Bool     `tfsdk:"flatcar_disable_auto_update"`
	OperatingSystemProfile   types.String   `tfsdk:"operating_system_profile"`
	NodeLabels               types.Map      `tfsdk:"node_labels"`
	NodeAnnotations          types.Map      `tfsdk:"node_annotations"`
	Taints                   []taintModel   `tfsdk:"taints"`
	OpenStack                *openstackNode `tfsdk:"openstack"`
	WaitForReady             types.Bool     `tfsdk:"wait_for_ready"`
	ReadyReplicas            types.Int64    `tfsdk:"ready_replicas"`
	AvailableReplicas        types.Int64    `tfsdk:"available_replicas"`
	CreationTimestamp        types.String   `tfsdk:"creation_timestamp"`
	Timeouts                 timeouts.Value `tfsdk:"timeouts"`
}

type taintModel struct {
	Key    types.String `tfsdk:"key"`
	Value  types.String `tfsdk:"value"`
	Effect types.String `tfsdk:"effect"`
}

type openstackNode struct {
	Flavor                    types.String `tfsdk:"flavor"`
	Image                     types.String `tfsdk:"image"`
	DiskSize                  types.Int64  `tfsdk:"disk_size"`
	AvailabilityZone          types.String `tfsdk:"availability_zone"`
	ServerGroup               types.String `tfsdk:"server_group"`
	UseFloatingIP             types.Bool   `tfsdk:"use_floating_ip"`
	ConfigDrive               types.Bool   `tfsdk:"config_drive"`
	InstanceReadyCheckPeriod  types.String `tfsdk:"instance_ready_check_period"`
	InstanceReadyCheckTimeout types.String `tfsdk:"instance_ready_check_timeout"`
	Tags                      types.Map    `tfsdk:"tags"`
}

func (r *machineDeploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_machine_deployment"
}

func (r *machineDeploymentResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Optional: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A machine deployment: a group of worker nodes of a cluster. KKP refuses machine deployments until the " +
			"cluster's control plane is healthy, so the provider retries creation until then. Changing the node template " +
			"(image, flavor, operating system, labels, kubelet version, …) makes machine-controller replace the nodes one by one.",
		Attributes: map[string]schema.Attribute{
			"id":         idAttribute("Machine deployment ID (KKP uses the name)."),
			"project_id": projectIDAttribute(),
			"cluster_id": replaceString(true, "Cluster ID (`kkp_cluster.id`)."),
			"name": schema.StringAttribute{Required: true, MarkdownDescription: "Name, a DNS label.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.LengthBetween(1, 63),
					stringvalidator.RegexMatches(dnsLabel, "must be lowercase letters, digits and hyphens, starting and ending with a letter or digit")}},
			"replicas":     schema.Int64Attribute{Required: true, MarkdownDescription: "Number of nodes.", Validators: []validator.Int64{int64validator.Between(0, 1000)}},
			"min_replicas": schema.Int64Attribute{Optional: true, MarkdownDescription: "Lower bound for the cluster autoscaler. Set together with `max_replicas`.", Validators: []validator.Int64{int64validator.AtLeast(0)}},
			"max_replicas": schema.Int64Attribute{Optional: true, MarkdownDescription: "Upper bound for the cluster autoscaler.", Validators: []validator.Int64{int64validator.AtLeast(1)}},
			"paused":       defaultBool("Pause rollouts of template changes.", false),
			"kubelet_version": computedString("Kubelet version. Defaults to the cluster's version at creation; it must not be newer "+
				"than the control plane, nor more than three minor versions older.", false),
			"operating_system": schema.StringAttribute{Required: true, MarkdownDescription: "`ubuntu`, `flatcar`, `rhel` or `rockylinux`.",
				Validators: []validator.String{stringvalidator.OneOf("ubuntu", "flatcar", "rhel", "rockylinux")}},
			"dist_upgrade_on_boot":        defaultBool("Upgrade all packages on first boot (Ubuntu, RHEL, Rocky Linux).", false),
			"flatcar_disable_auto_update": defaultBool("Disable Flatcar's automatic updates.", false),
			"operating_system_profile":    str("Operating System Manager profile to use instead of the default for the OS."),
			"node_labels":                 schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Kubernetes labels for the nodes."},
			"node_annotations":            schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Kubernetes annotations for the nodes."},
			"taints": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Taints for the nodes.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"key":    schema.StringAttribute{Required: true},
					"value":  schema.StringAttribute{Optional: true},
					"effect": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("NoSchedule", "PreferNoSchedule", "NoExecute")}},
				}},
			},
			"openstack": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "OpenStack machine settings.",
				Attributes: map[string]schema.Attribute{
					"flavor":                       schema.StringAttribute{Required: true, MarkdownDescription: "Flavor name."},
					"image":                        schema.StringAttribute{Required: true, MarkdownDescription: "Image name or ID. The datacenter's default images are listed by `kkp_datacenter`."},
					"disk_size":                    schema.Int64Attribute{Optional: true, MarkdownDescription: "Root volume size in GB. Unset uses the flavor's local disk.", Validators: []validator.Int64{int64validator.AtLeast(1)}},
					"availability_zone":            str("Availability zone."),
					"server_group":                 str("Server group ID, e.g. for anti-affinity."),
					"use_floating_ip":              defaultBool("Give every node a floating IP.", false),
					"config_drive":                 defaultBool("Use a config drive instead of the metadata service.", false),
					"instance_ready_check_period":  str("How often machine-controller checks a new instance, e.g. `5s`."),
					"instance_ready_check_timeout": str("How long machine-controller waits for a new instance, e.g. `2m`."),
					"tags":                         schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "OpenStack server tags (metadata)."},
				},
			},
			"wait_for_ready":     defaultBool("Wait until all replicas are available after create and update.", true),
			"ready_replicas":     schema.Int64Attribute{Computed: true},
			"available_replicas": schema.Int64Attribute{Computed: true},
			"creation_timestamp": idAttribute(""),
			"timeouts":           timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *machineDeploymentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg machineDeploymentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if known(cfg.MinReplicas) != known(cfg.MaxReplicas) && !cfg.MinReplicas.IsUnknown() && !cfg.MaxReplicas.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("min_replicas"), "Incomplete autoscaling", "Set both min_replicas and max_replicas, or neither.")
		return
	}
	if known(cfg.MinReplicas) && known(cfg.MaxReplicas) && known(cfg.Replicas) {
		lo, hi, n := cfg.MinReplicas.ValueInt64(), cfg.MaxReplicas.ValueInt64(), cfg.Replicas.ValueInt64()
		if lo > hi {
			resp.Diagnostics.AddAttributeError(path.Root("min_replicas"), "Invalid autoscaling bounds", "min_replicas must not exceed max_replicas.")
		} else if n < lo || n > hi {
			resp.Diagnostics.AddAttributeError(path.Root("replicas"), "Replicas out of bounds", "replicas must be between min_replicas and max_replicas, as KKP checks.")
		}
	}
	if known(cfg.FlatcarDisableAutoUpdate) && cfg.FlatcarDisableAutoUpdate.ValueBool() && known(cfg.OperatingSystem) && cfg.OperatingSystem.ValueString() != "flatcar" {
		resp.Diagnostics.AddAttributeError(path.Root("flatcar_disable_auto_update"), "Flatcar only", "flatcar_disable_auto_update only applies to operating_system = \"flatcar\".")
	}
}

func int32p(v types.Int64) *int32 {
	if !known(v) {
		return nil
	}
	return ptr(int32(v.ValueInt64()))
}

func (r *machineDeploymentResource) operatingSystem(m *machineDeploymentModel) client.OperatingSystem {
	up := &client.DistUpgrade{DistUpgradeOnBoot: m.DistUpgradeOnBoot.ValueBool()}
	switch m.OperatingSystem.ValueString() {
	case "flatcar":
		return client.OperatingSystem{Flatcar: &client.Flatcar{DisableAutoUpdate: m.FlatcarDisableAutoUpdate.ValueBool()}}
	case "rhel":
		return client.OperatingSystem{RHEL: up}
	case "rockylinux":
		return client.OperatingSystem{RockyLinux: up}
	default:
		return client.OperatingSystem{Ubuntu: up}
	}
}

func (r *machineDeploymentResource) template(ctx context.Context, m *machineDeploymentModel, diags *diagsT) client.NodeSpec {
	os := m.OpenStack
	var disk *int64
	if known(os.DiskSize) {
		disk = ptr(os.DiskSize.ValueInt64())
	}
	taints := make([]client.Taint, 0, len(m.Taints))
	for _, t := range m.Taints {
		taints = append(taints, client.Taint{Key: t.Key.ValueString(), Value: t.Value.ValueString(), Effect: t.Effect.ValueString()})
	}
	return client.NodeSpec{
		Cloud: client.NodeCloudSpec{OpenStack: &client.OpenStackNode{
			Flavor: os.Flavor.ValueString(), Image: os.Image.ValueString(), DiskSize: disk,
			AvailabilityZone: os.AvailabilityZone.ValueString(), ServerGroup: os.ServerGroup.ValueString(),
			UseFloatingIP: os.UseFloatingIP.ValueBool(), ConfigDrive: os.ConfigDrive.ValueBool(),
			InstanceReadyCheckPeriod: os.InstanceReadyCheckPeriod.ValueString(), InstanceReadyCheckTimeout: os.InstanceReadyCheckTimeout.ValueString(),
			Tags: goMap(ctx, os.Tags, diags),
		}},
		OperatingSystem: r.operatingSystem(m),
		Versions:        client.NodeVersions{Kubelet: m.KubeletVersion.ValueString()},
		Labels:          goMap(ctx, m.NodeLabels, diags),
		Annotations:     goMap(ctx, m.NodeAnnotations, diags),
		Taints:          taints,
	}
}

func (r *machineDeploymentResource) fill(m *machineDeploymentModel, projectID, clusterID string, md *client.NodeDeployment) {
	s, t := md.Spec, md.Spec.Template
	m.ID = types.StringValue(md.ID)
	m.ProjectID = types.StringValue(projectID)
	m.ClusterID = types.StringValue(clusterID)
	m.Name = types.StringValue(md.Name)
	m.Replicas = types.Int64Value(int64(s.Replicas))
	i64 := func(p *int32) types.Int64 {
		if p == nil {
			return types.Int64Null()
		}
		return types.Int64Value(int64(*p))
	}
	m.MinReplicas, m.MaxReplicas = i64(s.MinReplicas), i64(s.MaxReplicas)
	m.Paused = types.BoolValue(s.Paused != nil && *s.Paused)
	m.KubeletVersion = types.StringValue(t.Versions.Kubelet)
	switch o := t.OperatingSystem; {
	case o.Flatcar != nil:
		m.OperatingSystem = types.StringValue("flatcar")
		m.FlatcarDisableAutoUpdate = types.BoolValue(o.Flatcar.DisableAutoUpdate)
	case o.RHEL != nil:
		m.OperatingSystem = types.StringValue("rhel")
		m.DistUpgradeOnBoot = types.BoolValue(o.RHEL.DistUpgradeOnBoot)
	case o.RockyLinux != nil:
		m.OperatingSystem = types.StringValue("rockylinux")
		m.DistUpgradeOnBoot = types.BoolValue(o.RockyLinux.DistUpgradeOnBoot)
	case o.Ubuntu != nil:
		m.OperatingSystem = types.StringValue("ubuntu")
		m.DistUpgradeOnBoot = types.BoolValue(o.Ubuntu.DistUpgradeOnBoot)
	}
	// Imported resources have no value for these yet.
	for b, def := range map[*types.Bool]bool{&m.DistUpgradeOnBoot: false, &m.FlatcarDisableAutoUpdate: false, &m.WaitForReady: true} {
		if b.IsNull() || b.IsUnknown() {
			*b = types.BoolValue(def)
		}
	}
	m.OperatingSystemProfile = strOrNull(md.Annotations[ospAnnotation])
	m.NodeLabels = mapKeep(t.Labels, m.NodeLabels)
	m.NodeAnnotations = mapKeep(t.Annotations, m.NodeAnnotations)
	if len(t.Taints) == 0 && m.Taints != nil && len(m.Taints) == 0 {
		// keep an explicit empty list
	} else if len(t.Taints) == 0 {
		m.Taints = nil
	} else {
		m.Taints = make([]taintModel, len(t.Taints))
		for i, tt := range t.Taints {
			m.Taints[i] = taintModel{Key: types.StringValue(tt.Key), Value: strOrNull(tt.Value), Effect: types.StringValue(tt.Effect)}
		}
	}
	if m.OpenStack == nil {
		m.OpenStack = &openstackNode{Tags: types.MapNull(types.StringType)}
	}
	if os := t.Cloud.OpenStack; os != nil {
		n := m.OpenStack
		n.Flavor, n.Image = types.StringValue(os.Flavor), types.StringValue(os.Image)
		if os.DiskSize != nil && *os.DiskSize > 0 {
			n.DiskSize = types.Int64Value(*os.DiskSize)
		} else {
			n.DiskSize = types.Int64Null()
		}
		n.AvailabilityZone, n.ServerGroup = strOrNull(os.AvailabilityZone), strOrNull(os.ServerGroup)
		n.UseFloatingIP, n.ConfigDrive = types.BoolValue(os.UseFloatingIP), types.BoolValue(os.ConfigDrive)
		n.InstanceReadyCheckPeriod, n.InstanceReadyCheckTimeout = strOrNull(os.InstanceReadyCheckPeriod), strOrNull(os.InstanceReadyCheckTimeout)
		n.Tags = mapKeep(os.Tags, n.Tags)
	}
	m.ReadyReplicas = types.Int64Value(int64(md.Status.ReadyReplicas))
	m.AvailableReplicas = types.Int64Value(int64(md.Status.AvailableReplicas))
	m.CreationTimestamp = types.StringValue(md.CreationTimestamp)
}

func (r *machineDeploymentResource) waitReady(ctx context.Context, m *machineDeploymentModel) error {
	p, c, id := m.ProjectID.ValueString(), m.ClusterID.ValueString(), m.ID.ValueString()
	return waitFor(ctx, "machine deployment replicas to become available", func() (bool, error) {
		md, err := r.client().GetMachineDeployment(ctx, p, c, id)
		if err != nil {
			return false, err
		}
		r.fill(m, p, c, md)
		return md.Status.AvailableReplicas >= md.Spec.Replicas && md.Status.UpdatedReplicas >= md.Spec.Replicas, nil
	})
}

func (r *machineDeploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan machineDeploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	project := r.project(plan.ProjectID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Create(ctx, 30*time.Minute)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	clusterID := plan.ClusterID.ValueString()

	md := client.NodeDeployment{
		ObjectMeta: client.ObjectMeta{Name: plan.Name.ValueString()},
		Spec: client.NodeDeploymentSpec{
			Replicas:    int32(plan.Replicas.ValueInt64()),
			MinReplicas: int32p(plan.MinReplicas),
			MaxReplicas: int32p(plan.MaxReplicas),
			Template:    r.template(ctx, &plan, &resp.Diagnostics),
		},
	}
	if plan.Paused.ValueBool() {
		md.Spec.Paused = ptr(true)
	}
	if v := plan.OperatingSystemProfile.ValueString(); v != "" {
		md.Annotations = map[string]string{ospAnnotation: v}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	var created *client.NodeDeployment
	err := waitFor(ctx, "the cluster to accept machine deployments", func() (bool, error) {
		var err error
		created, err = r.client().CreateMachineDeployment(ctx, project, clusterID, md)
		if client.IsUnavailable(err) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create machine deployment", err)
		return
	}
	state := plan
	r.fill(&state, project, clusterID, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)

	if plan.WaitForReady.ValueBool() {
		if err := r.waitReady(ctx, &state); err != nil {
			addAPIError(&resp.Diagnostics, "Machine deployment did not become ready", err)
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	}
}

func (r *machineDeploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state machineDeploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, c := state.ProjectID.ValueString(), state.ClusterID.ValueString()
	md, err := r.client().GetMachineDeployment(ctx, p, c, state.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && md.DeletionTimestamp != "") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read machine deployment", err)
		return
	}
	r.fill(&state, p, c, md)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func nullable[T any](v T, set bool) any {
	if !set {
		return nil
	}
	return v
}

func (r *machineDeploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state machineDeploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := plan.Timeouts.Update(ctx, 30*time.Minute)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	diags := &resp.Diagnostics
	p, c, id := state.ProjectID.ValueString(), state.ClusterID.ValueString(), state.ID.ValueString()

	// The template is sent whole, except for maps: a merge patch merges maps, so removed keys are sent as null.
	tpl := r.template(ctx, &plan, diags)
	os := tpl.Cloud.OpenStack
	osPatch := map[string]any{
		"flavor": os.Flavor, "image": os.Image, "diskSize": nullable(os.DiskSize, os.DiskSize != nil),
		"availabilityZone": nullable(os.AvailabilityZone, os.AvailabilityZone != ""), "serverGroup": nullable(os.ServerGroup, os.ServerGroup != ""),
		"useFloatingIP": os.UseFloatingIP, "configDrive": os.ConfigDrive,
		"instanceReadyCheckPeriod":  nullable(os.InstanceReadyCheckPeriod, os.InstanceReadyCheckPeriod != ""),
		"instanceReadyCheckTimeout": nullable(os.InstanceReadyCheckTimeout, os.InstanceReadyCheckTimeout != ""),
		"tags":                      mapPatch(goMap(ctx, state.OpenStack.Tags, diags), os.Tags),
	}
	taints := any(nil)
	if len(tpl.Taints) > 0 {
		taints = tpl.Taints
	}
	spec := map[string]any{
		"replicas":    plan.Replicas.ValueInt64(),
		"minReplicas": nullable(plan.MinReplicas.ValueInt64(), known(plan.MinReplicas)),
		"maxReplicas": nullable(plan.MaxReplicas.ValueInt64(), known(plan.MaxReplicas)),
		"paused":      plan.Paused.ValueBool(),
		"template": map[string]any{
			"cloud":           map[string]any{"openstack": osPatch},
			"operatingSystem": tpl.OperatingSystem,
			"versions":        tpl.Versions,
			"labels":          mapPatch(goMap(ctx, state.NodeLabels, diags), tpl.Labels),
			"annotations":     mapPatch(goMap(ctx, state.NodeAnnotations, diags), tpl.Annotations),
			"taints":          taints,
		},
	}
	patch := map[string]any{"spec": spec}
	if !plan.OperatingSystemProfile.Equal(state.OperatingSystemProfile) {
		patch["annotations"] = map[string]any{ospAnnotation: nullable(plan.OperatingSystemProfile.ValueString(), plan.OperatingSystemProfile.ValueString() != "")}
	}
	if diags.HasError() {
		return
	}
	md, err := r.client().PatchMachineDeployment(ctx, p, c, id, patch)
	if err != nil {
		addAPIError(diags, "Cannot update machine deployment", err)
		return
	}
	r.fill(&plan, p, c, md)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if plan.WaitForReady.ValueBool() && !plan.Paused.ValueBool() {
		if err := r.waitReady(ctx, &plan); err != nil {
			addAPIError(diags, "Machine deployment did not become ready", err)
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *machineDeploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state machineDeploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, d := state.Timeouts.Delete(ctx, 30*time.Minute)
	resp.Diagnostics.Append(d...)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p, c, id := state.ProjectID.ValueString(), state.ClusterID.ValueString(), state.ID.ValueString()
	if err := r.client().DeleteMachineDeployment(ctx, p, c, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		addAPIError(&resp.Diagnostics, "Cannot delete machine deployment", err)
		return
	}
	err := waitGone(ctx, "machine deployment", func() error { _, err := r.client().GetMachineDeployment(ctx, p, c, id); return err })
	if err != nil {
		addAPIError(&resp.Diagnostics, "Machine deployment was not deleted", err)
	}
}

func (r *machineDeploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 3, "<project_id>/<cluster_id>/<machine_deployment_id>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	for i, k := range []string{"project_id", "cluster_id", "id"} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(k), parts[i])...)
	}
}
