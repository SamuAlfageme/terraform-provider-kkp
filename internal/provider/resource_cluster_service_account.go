package provider

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
)

// tokenWait is how long Create waits for the user cluster's token controller to fill in the token secret.
var tokenWait = 2 * time.Minute

var _ resource.ResourceWithImportState = (*clusterServiceAccountResource)(nil)

func newClusterServiceAccountResource() resource.Resource { return &clusterServiceAccountResource{} }

type clusterServiceAccountResource struct{ base }

type clusterServiceAccountModel struct {
	ID                types.String `tfsdk:"id"`
	ProjectID         types.String `tfsdk:"project_id"`
	ClusterID         types.String `tfsdk:"cluster_id"`
	Name              types.String `tfsdk:"name"`
	Namespace         types.String `tfsdk:"namespace"`
	ClusterRoles      types.Set    `tfsdk:"cluster_roles"`
	Kubeconfig        types.String `tfsdk:"kubeconfig"`
	CreationTimestamp types.String `tfsdk:"creation_timestamp"`
}

func (r *clusterServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_service_account"
}

func (r *clusterServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Kubernetes service account in a user cluster, created through the KKP API like the Service Accounts tab of the " +
			"KKP dashboard, with cluster role bindings and a kubeconfig that uses its token. It works where admin " +
			"kubeconfigs are disabled and the cluster has no OIDC. The kubeconfig ends up in state, so treat the state as a secret. " +
			"The cluster must be up: create it after the `kkp_cluster`.",
		Attributes: map[string]schema.Attribute{
			"id":         idAttribute("`<namespace>/<name>`."),
			"project_id": projectIDAttribute(),
			"cluster_id": replaceString(true, "Cluster ID (`kkp_cluster.id`)."),
			"name": schema.StringAttribute{Required: true, MarkdownDescription: "Service account name, a DNS label.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.LengthBetween(1, 63),
					stringvalidator.RegexMatches(dnsLabel, "must be lowercase letters, digits and hyphens, starting and ending with a letter or digit")}},
			"namespace": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("kube-system"),
				MarkdownDescription: "Namespace of the service account. It must exist. Defaults to `kube-system`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"cluster_roles": schema.SetAttribute{Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Cluster roles to bind the service account to, e.g. `[\"cluster-admin\"]`. KKP manages bindings for " +
					"`cluster-admin`, `edit` and `view`, and for cluster roles labelled `component=userClusterRole`. Changes are made in place."},
			"kubeconfig": schema.StringAttribute{Computed: true, Sensitive: true,
				MarkdownDescription: "Kubeconfig YAML with the service account's token, for the `kubernetes` and `helm` providers or `kubectl`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"creation_timestamp": idAttribute(""),
		},
	}
}

func clusterSAID(namespace, name string) string { return namespace + "/" + name }

func (r *clusterServiceAccountResource) subject(m *clusterServiceAccountModel) client.ClusterRoleUser {
	return client.ClusterRoleUser{ServiceAccount: m.Name.ValueString(), ServiceAccountNamespace: m.Namespace.ValueString()}
}

// bind adds the roles; a role the account already has (KKP answers 400 "already connected") counts as done.
func (r *clusterServiceAccountResource) bind(ctx context.Context, m *clusterServiceAccountModel, roles []string) error {
	for _, role := range roles {
		err := r.client().BindClusterRole(ctx, m.ProjectID.ValueString(), m.ClusterID.ValueString(), role, r.subject(m))
		if err != nil && !strings.Contains(err.Error(), "already connected") {
			return err
		}
	}
	return nil
}

func (r *clusterServiceAccountResource) unbind(ctx context.Context, m *clusterServiceAccountModel, roles []string) error {
	for _, role := range roles {
		if err := r.client().UnbindClusterRole(ctx, m.ProjectID.ValueString(), m.ClusterID.ValueString(), role, r.subject(m)); err != nil {
			return err
		}
	}
	return nil
}

func (r *clusterServiceAccountResource) clusterRoles(ctx context.Context, m *clusterServiceAccountModel) ([]string, error) {
	perms, err := r.client().GetClusterServiceAccountPermissions(ctx, m.ProjectID.ValueString(), m.ClusterID.ValueString(),
		m.Namespace.ValueString(), m.Name.ValueString())
	if err != nil {
		return nil, err
	}
	var roles []string
	for _, p := range perms {
		if p.Scope == client.PermissionCluster {
			roles = append(roles, p.RoleRefName)
		}
	}
	slices.Sort(roles)
	return slices.Compact(roles), nil
}

// kubeconfig waits for the token secret: KKP creates it with the service account, Kubernetes fills it in.
func (r *clusterServiceAccountResource) kubeconfig(ctx context.Context, m *clusterServiceAccountModel) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tokenWait)
	defer cancel()
	var kc string
	err := waitFor(ctx, "the service account token", func() (bool, error) {
		var err error
		kc, err = r.client().GetClusterServiceAccountKubeconfig(ctx, m.ProjectID.ValueString(), m.ClusterID.ValueString(),
			m.Namespace.ValueString(), m.Name.ValueString())
		if err != nil && (strings.Contains(err.Error(), "has no secret") || strings.Contains(err.Error(), "no token defined")) {
			return false, nil
		}
		return err == nil, err
	})
	return kc, err
}

func (r *clusterServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan clusterServiceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	project := r.project(plan.ProjectID, &resp.Diagnostics)
	roles := goStrings(ctx, plan.ClusterRoles, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ProjectID = types.StringValue(project)
	sa, err := r.client().CreateClusterServiceAccount(ctx, project, plan.ClusterID.ValueString(), plan.Namespace.ValueString(), plan.Name.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot create cluster service account", err)
		return
	}
	plan.ID = types.StringValue(clusterSAID(plan.Namespace.ValueString(), plan.Name.ValueString()))
	plan.CreationTimestamp = types.StringValue(sa.CreationTimestamp)
	// Save the account first, so a failed binding or token wait leaves it in state to be fixed or destroyed.
	plan.Kubeconfig = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)

	if err := r.bind(ctx, &plan, roles); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot bind cluster service account", err)
		return
	}
	kc, err := r.kubeconfig(ctx, &plan)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot get the cluster service account's kubeconfig", err)
		return
	}
	plan.Kubeconfig = types.StringValue(kc)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *clusterServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state clusterServiceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sa, err := r.client().GetClusterServiceAccount(ctx, state.ProjectID.ValueString(), state.ClusterID.ValueString(),
		state.Namespace.ValueString(), state.Name.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster service account", err)
		return
	}
	roles, err := r.clusterRoles(ctx, &state)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Cannot read cluster service account permissions", err)
		return
	}
	state.CreationTimestamp = types.StringValue(sa.CreationTimestamp)
	state.ClusterRoles = setValue(roles, state.ClusterRoles)
	// The token does not change; fetch it only when state has none (after an import or a failed create).
	if state.Kubeconfig.IsNull() || state.Kubeconfig.ValueString() == "" {
		kc, err := r.kubeconfig(ctx, &state)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Cannot get the cluster service account's kubeconfig", err)
			return
		}
		state.Kubeconfig = types.StringValue(kc)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *clusterServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state clusterServiceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	want := goStrings(ctx, plan.ClusterRoles, &resp.Diagnostics)
	have := goStrings(ctx, state.ClusterRoles, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	var add, remove []string
	for _, role := range want {
		if !slices.Contains(have, role) {
			add = append(add, role)
		}
	}
	for _, role := range have {
		if !slices.Contains(want, role) {
			remove = append(remove, role)
		}
	}
	if err := r.unbind(ctx, &plan, remove); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot unbind cluster service account", err)
		return
	}
	if err := r.bind(ctx, &plan, add); err != nil {
		addAPIError(&resp.Diagnostics, "Cannot bind cluster service account", err)
		return
	}
	plan.Kubeconfig = state.Kubeconfig
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete: KKP removes the account from every binding before deleting it, and the token secret goes with it.
func (r *clusterServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state clusterServiceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client().DeleteClusterServiceAccount(ctx, state.ProjectID.ValueString(), state.ClusterID.ValueString(),
		state.Namespace.ValueString(), state.Name.ValueString())
	if err != nil && !client.IsNotFound(err) {
		addAPIError(&resp.Diagnostics, "Cannot delete cluster service account", err)
	}
}

func (r *clusterServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitID(req.ID, 4, "<project_id>/<cluster_id>/<namespace>/<name>")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cluster_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("namespace"), parts[2])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[3])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), clusterSAID(parts[2], parts[3]))...)
}
