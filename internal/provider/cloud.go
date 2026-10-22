package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// The cloud specific parts of clusters (spec.cloud.<provider>) and machine deployments
// (spec.template.cloud.<provider>) are flat JSON objects of strings, booleans, numbers and maps. Each cloud is
// described by a table of fields; schema, request bodies, merge patches and state all come from that table.

type fieldKind int

const (
	kString fieldKind = iota
	kBool
	kInt
	kStringMap
	kStringList
	// kRanges is a list of CIDRs sent as {"cidrBlocks": [...]}.
	kRanges
	// kBase64 is a string sent base64 encoded (GCP service account JSON).
	kBase64
)

type cloudField struct {
	Name string // attribute name
	JSON string // key in the KKP object
	Kind fieldKind
	Desc string
	// Credential fields are never returned by KKP (it moves them into a secret), so state keeps the
	// configured value. When one changes, all of them are sent again.
	Credential bool
	Sensitive  bool
	Required   bool
	// Computed fields are filled in by KKP when left unset.
	Computed bool
	// Replace forces a new resource when the value changes: KKP ignores or rejects in-place changes.
	Replace bool
	// Default applies to booleans.
	Default    *bool
	Validators []validator.String
}

// credentialSet is one accepted way of authenticating: all Required fields, plus any of Optional.
type credentialSet struct {
	Required []string
	Optional []string
}

type cloudDef struct {
	// Name is the attribute name, the JSON key and the datacenter provider name.
	Name   string
	Title  string
	Fields []cloudField
	// CredentialSets is empty for node specs.
	CredentialSets []credentialSet
}

func (d cloudDef) attrTypes() map[string]attr.Type {
	out := map[string]attr.Type{}
	for _, f := range d.Fields {
		out[f.Name] = f.Kind.attrType()
	}
	return out
}

func (k fieldKind) attrType() attr.Type {
	switch k {
	case kBool:
		return types.BoolType
	case kInt:
		return types.Int64Type
	case kStringMap:
		return types.MapType{ElemType: types.StringType}
	case kStringList, kRanges:
		return types.ListType{ElemType: types.StringType}
	default:
		return types.StringType
	}
}

func (d cloudDef) null() types.Object { return types.ObjectNull(d.attrTypes()) }

// cloudReplaceIfSwitched replaces the resource when a cloud block appears or disappears.
var cloudReplaceIfSwitched = objectplanmodifier.RequiresReplaceIf(
	func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
		resp.RequiresReplace = !req.State.Raw.IsNull() && req.StateValue.IsNull() != req.PlanValue.IsNull()
	},
	"Changing the cloud provider replaces the resource.", "Changing the cloud provider replaces the resource.")

func (d cloudDef) schema(what string) schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{}
	for _, f := range d.Fields {
		desc := f.Desc
		if f.Credential {
			desc += " KKP stores credentials in a secret and never returns them, so changes made outside Terraform are not detected."
		}
		attrs[f.Name] = f.schema(desc)
	}
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: fmt.Sprintf("%s %s. Set the block of the datacenter's cloud provider, and only that one.", d.Title, what),
		Attributes:          attrs,
		PlanModifiers:       []planmodifier.Object{cloudReplaceIfSwitched},
	}
}

func (f cloudField) schema(desc string) schema.Attribute {
	optional, computed := !f.Required, f.Computed
	switch f.Kind {
	case kBool:
		a := schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: desc}
		if f.Default != nil {
			a.Default = booldefault.StaticBool(*f.Default)
		} else {
			a.PlanModifiers = append(a.PlanModifiers, boolplanmodifier.UseStateForUnknown())
		}
		if f.Replace {
			a.PlanModifiers = append(a.PlanModifiers, boolplanmodifier.RequiresReplace())
		}
		return a
	case kInt:
		a := schema.Int64Attribute{Required: f.Required, Optional: optional, Computed: computed, MarkdownDescription: desc}
		if computed {
			a.PlanModifiers = append(a.PlanModifiers, int64planmodifier.UseStateForUnknown())
		}
		if f.Replace {
			a.PlanModifiers = append(a.PlanModifiers, int64planmodifier.RequiresReplace())
		}
		return a
	case kStringMap:
		return schema.MapAttribute{Required: f.Required, Optional: optional, ElementType: types.StringType, MarkdownDescription: desc}
	case kStringList, kRanges:
		return schema.ListAttribute{Required: f.Required, Optional: optional, ElementType: types.StringType, MarkdownDescription: desc}
	default:
		a := schema.StringAttribute{Required: f.Required, Optional: optional, Computed: computed && !f.Credential,
			Sensitive: f.Sensitive, MarkdownDescription: desc, Validators: f.Validators}
		if a.Computed {
			a.PlanModifiers = append(a.PlanModifiers, stringplanmodifier.UseStateForUnknown())
		}
		if f.Replace {
			a.PlanModifiers = append(a.PlanModifiers, stringplanmodifier.RequiresReplace())
		}
		return a
	}
}

// jsonValue converts a known, non-null attribute value to what KKP expects. ok is false for null/unknown/empty.
func (f cloudField) jsonValue(ctx context.Context, v attr.Value) (any, bool) {
	if v == nil || v.IsNull() || v.IsUnknown() {
		return nil, false
	}
	switch f.Kind {
	case kBool:
		return v.(basetypes.BoolValue).ValueBool(), true //nolint:forcetypeassert // kind matches attrType
	case kInt:
		return v.(basetypes.Int64Value).ValueInt64(), true //nolint:forcetypeassert
	case kStringMap:
		m := map[string]string{}
		_ = v.(basetypes.MapValue).ElementsAs(ctx, &m, false) //nolint:forcetypeassert
		return m, true
	case kStringList, kRanges:
		var l []string
		_ = v.(basetypes.ListValue).ElementsAs(ctx, &l, false) //nolint:forcetypeassert
		if f.Kind == kRanges {
			if len(l) == 0 {
				return nil, false
			}
			return map[string]any{"cidrBlocks": l}, true
		}
		return l, true
	case kBase64:
		s := v.(basetypes.StringValue).ValueString() //nolint:forcetypeassert
		if s == "" {
			return nil, false
		}
		return base64.StdEncoding.EncodeToString([]byte(s)), true
	default:
		s := v.(basetypes.StringValue).ValueString() //nolint:forcetypeassert
		return s, s != ""
	}
}

func (f cloudField) fromJSON(raw any, prior attr.Value) attr.Value {
	keepEmpty := func(n int) bool {
		switch p := prior.(type) {
		case basetypes.MapValue:
			return n == 0 && !p.IsNull() && !p.IsUnknown() && len(p.Elements()) == 0
		case basetypes.ListValue:
			return n == 0 && !p.IsNull() && !p.IsUnknown() && len(p.Elements()) == 0
		}
		return false
	}
	switch f.Kind {
	case kBool:
		b, _ := raw.(bool)
		return types.BoolValue(b)
	case kInt:
		if n, ok := raw.(float64); ok && n != 0 {
			return types.Int64Value(int64(n))
		}
		return types.Int64Null()
	case kStringMap:
		m := map[string]string{}
		if rm, ok := raw.(map[string]any); ok {
			for k, v := range rm {
				m[k] = fmt.Sprint(v)
			}
		}
		if keepEmpty(len(m)) {
			return prior
		}
		return mapValue(m)
	case kStringList, kRanges:
		if f.Kind == kRanges {
			rm, _ := raw.(map[string]any)
			raw = rm["cidrBlocks"]
		}
		var l []string
		if rl, ok := raw.([]any); ok {
			for _, v := range rl {
				l = append(l, fmt.Sprint(v))
			}
		}
		if keepEmpty(len(l)) {
			return prior
		}
		return listValue(l, types.ListNull(types.StringType))
	default:
		s, _ := raw.(string)
		return strOrNull(s)
	}
}

func attrs(o types.Object) map[string]attr.Value {
	if o.IsNull() || o.IsUnknown() {
		return map[string]attr.Value{}
	}
	return o.Attributes()
}

// body is the JSON object for a create request.
func (d cloudDef) body(ctx context.Context, o types.Object) map[string]any {
	out := map[string]any{}
	vals := attrs(o)
	for _, f := range d.Fields {
		if v, ok := f.jsonValue(ctx, vals[f.Name]); ok {
			out[f.JSON] = v
		}
	}
	return out
}

// fromJSON builds the block from what KKP returned, keeping credentials from prior.
func (d cloudDef) fromJSON(raw map[string]any, prior types.Object) types.Object {
	p := attrs(prior)
	vals := map[string]attr.Value{}
	for _, f := range d.Fields {
		if f.Credential {
			if v, ok := p[f.Name]; ok && !v.IsUnknown() {
				vals[f.Name] = v
			} else {
				vals[f.Name] = types.StringNull()
			}
			continue
		}
		vals[f.Name] = f.fromJSON(raw[f.JSON], p[f.Name])
	}
	return types.ObjectValueMust(d.attrTypes(), vals)
}

// patch is the merge patch from state to plan. With full, every field is sent (machine deployment templates
// are rebuilt from the patch); otherwise only changed, in-place updatable fields are.
func (d cloudDef) patch(ctx context.Context, plan, state types.Object, full bool) map[string]any {
	pv, sv := attrs(plan), attrs(state)
	out := map[string]any{}
	credsChanged := false
	for _, f := range d.Fields {
		if f.Credential && !pv[f.Name].Equal(sv[f.Name]) {
			credsChanged = true
		}
	}
	for _, f := range d.Fields {
		changed := pv[f.Name] != nil && !pv[f.Name].Equal(sv[f.Name])
		switch {
		case f.Credential && !credsChanged, !f.Credential && !full && (!changed || f.Replace):
			continue
		case f.Kind == kStringMap:
			oldM, _ := f.jsonValue(ctx, sv[f.Name])
			newM, _ := f.jsonValue(ctx, pv[f.Name])
			o, _ := oldM.(map[string]string)
			n, _ := newM.(map[string]string)
			if p := mapPatch(o, n); len(p) > 0 {
				out[f.JSON] = p
			}
		default:
			if pv[f.Name] != nil && pv[f.Name].IsUnknown() {
				continue
			}
			v, ok := f.jsonValue(ctx, pv[f.Name])
			if !ok {
				v = nil
			}
			out[f.JSON] = v
		}
	}
	return out
}

func (d cloudDef) setFields(o types.Object) []string {
	var out []string
	vals := attrs(o)
	for _, f := range d.Fields {
		if !f.Credential {
			continue
		}
		if v, ok := vals[f.Name].(basetypes.StringValue); ok && !v.IsNull() && v.ValueString() != "" {
			out = append(out, f.Name)
		}
	}
	return out
}

func (d cloudDef) hasUnknown(o types.Object) bool {
	if o.IsUnknown() {
		return true
	}
	for _, v := range attrs(o) {
		if v.IsUnknown() {
			return true
		}
	}
	return false
}

// validateCredentials checks the block against the accepted credential sets.
func (d cloudDef) validateCredentials(o types.Object, preset bool, diags *diagsT) {
	if d.hasUnknown(o) {
		return
	}
	used := d.setFields(o)
	p := path.Root(d.Name)
	switch {
	case preset && len(used) > 0:
		diags.AddAttributeError(path.Root("credential_preset"), "Conflicting credentials", "Set either credential_preset or credentials in "+d.Name+", not both.")
		return
	case preset:
		return
	case len(used) == 0:
		diags.AddAttributeError(p, "Missing credentials", "Set credential_preset or credentials in "+d.Name+": "+d.credentialHelp()+".")
		return
	}
	partial := false
	for _, s := range d.CredentialSets {
		allowed := append(slices.Clone(s.Required), s.Optional...)
		within := true
		for _, u := range used {
			within = within && slices.Contains(allowed, u)
		}
		complete := true
		for _, r := range s.Required {
			complete = complete && slices.Contains(used, r)
		}
		if within && complete {
			return
		}
		partial = partial || within
	}
	if partial {
		diags.AddAttributeError(p, "Incomplete credentials", "Set "+d.credentialHelp()+".")
	} else {
		diags.AddAttributeError(p, "Conflicting credentials", "Use exactly one way to authenticate: "+d.credentialHelp()+".")
	}
}

func (d cloudDef) credentialHelp() string {
	var alts []string
	for _, s := range d.CredentialSets {
		alts = append(alts, strings.Join(s.Required, ", "))
	}
	return strings.Join(alts, "; or ")
}

// exactlyOneCloud reports the configured cloud, or adds an error.
func exactlyOneCloud(defs []cloudDef, blocks map[string]types.Object, diags *diagsT) (cloudDef, bool) {
	var set []cloudDef
	for _, d := range defs {
		o := blocks[d.Name]
		if o.IsUnknown() {
			return cloudDef{}, false
		}
		if !o.IsNull() {
			set = append(set, d)
		}
	}
	if len(set) == 1 {
		return set[0], true
	}
	var names []string
	for _, d := range defs {
		names = append(names, "`"+d.Name+"`")
	}
	diags.AddError("Exactly one cloud provider block required",
		fmt.Sprintf("Set exactly one of %s, matching the datacenter's cloud provider (it may be empty, e.g. `openstack = {}` with a credential preset). Got %d.",
			strings.Join(names, ", "), len(set)))
	return cloudDef{}, false
}

func b(v bool) *bool { return &v }

// Cluster cloud specs (spec.cloud.<provider>).
var clusterClouds = []cloudDef{
	{
		Name: "openstack", Title: "OpenStack",
		Fields: []cloudField{
			{Name: "username", JSON: "username", Credential: true, Desc: "OpenStack user name."},
			{Name: "password", JSON: "password", Credential: true, Sensitive: true, Desc: "OpenStack password."},
			{Name: "domain", JSON: "domain", Credential: true, Desc: "OpenStack domain."},
			{Name: "project", JSON: "project", Credential: true, Desc: "OpenStack project (tenant) name."},
			{Name: "project_id", JSON: "projectID", Credential: true, Desc: "OpenStack project (tenant) ID."},
			{Name: "application_credential_id", JSON: "applicationCredentialID", Credential: true, Desc: "Application credential ID."},
			{Name: "application_credential_secret", JSON: "applicationCredentialSecret", Credential: true, Sensitive: true, Desc: "Application credential secret."},
			{Name: "floating_ip_pool", JSON: "floatingIPPool", Computed: true, Replace: true, Desc: "External network for floating IPs. KKP picks one when unset."},
			{Name: "network", JSON: "network", Computed: true, Replace: true, Desc: "Existing network for the nodes. KKP creates one when unset."},
			{Name: "subnet_id", JSON: "subnetID", Computed: true, Replace: true, Desc: "Existing subnet for the nodes. KKP creates one when unset."},
			{Name: "router_id", JSON: "routerID", Computed: true, Replace: true, Desc: "Existing router. KKP creates one when unset."},
			{Name: "security_groups", JSON: "securityGroups", Computed: true, Replace: true, Desc: "Existing security group for the nodes. KKP creates one when unset."},
			{Name: "node_ports_allowed_ip_ranges", JSON: "nodePortsAllowedIPRanges", Kind: kRanges, Desc: "CIDRs allowed to reach NodePort services."},
		},
		CredentialSets: []credentialSet{
			{Required: []string{"username", "password", "domain", "project"}, Optional: []string{"project_id"}},
			{Required: []string{"username", "password", "domain", "project_id"}, Optional: []string{"project"}},
			{Required: []string{"application_credential_id", "application_credential_secret"}},
		},
	},
	{
		Name: "aws", Title: "AWS",
		Fields: []cloudField{
			{Name: "access_key_id", JSON: "accessKeyID", Credential: true, Desc: "Access key ID."},
			{Name: "secret_access_key", JSON: "secretAccessKey", Credential: true, Sensitive: true, Desc: "Secret access key."},
			{Name: "assume_role_arn", JSON: "assumeRoleARN", Credential: true, Desc: "IAM role to assume for managing AWS resources."},
			{Name: "assume_role_external_id", JSON: "assumeRoleExternalID", Credential: true, Desc: "External ID for assuming `assume_role_arn`."},
			{Name: "vpc_id", JSON: "vpcID", Computed: true, Replace: true, Desc: "Existing VPC. KKP uses the region's default VPC when unset."},
			{Name: "security_group_id", JSON: "securityGroupID", Computed: true, Replace: true, Desc: "Existing security group for the nodes. KKP creates one when unset."},
			{Name: "route_table_id", JSON: "routeTableID", Computed: true, Replace: true, Desc: "Existing route table. KKP uses the VPC's main route table when unset."},
			{Name: "instance_profile_name", JSON: "instanceProfileName", Computed: true, Replace: true, Desc: "Existing IAM instance profile for the nodes. KKP creates one when unset."},
			{Name: "role_arn", JSON: "roleARN", Computed: true, Replace: true, Desc: "IAM role for the control plane. KKP creates one when unset."},
			{Name: "disable_iam_reconciling", JSON: "disableIAMReconciling", Kind: kBool, Default: b(false), Replace: true, Desc: "Let KKP leave IAM resources alone (use with existing roles and profiles)."},
			{Name: "node_ports_allowed_ip_ranges", JSON: "nodePortsAllowedIPRanges", Kind: kRanges, Desc: "CIDRs allowed to reach NodePort services."},
		},
		CredentialSets: []credentialSet{
			{Required: []string{"access_key_id", "secret_access_key"}, Optional: []string{"assume_role_arn", "assume_role_external_id"}},
		},
	},
	{
		Name: "azure", Title: "Azure",
		Fields: []cloudField{
			{Name: "tenant_id", JSON: "tenantID", Credential: true, Desc: "Entra ID (Azure AD) tenant."},
			{Name: "subscription_id", JSON: "subscriptionID", Credential: true, Desc: "Subscription."},
			{Name: "client_id", JSON: "clientID", Credential: true, Desc: "Service principal client ID."},
			{Name: "client_secret", JSON: "clientSecret", Credential: true, Sensitive: true, Desc: "Service principal client secret."},
			{Name: "resource_group", JSON: "resourceGroup", Computed: true, Replace: true, Desc: "Resource group for the cluster's resources. KKP creates one when unset."},
			{Name: "vnet_resource_group", JSON: "vnetResourceGroup", Computed: true, Replace: true, Desc: "Separate resource group of the VNet."},
			{Name: "vnet", JSON: "vnet", Computed: true, Replace: true, Desc: "Existing VNet. KKP creates one when unset."},
			{Name: "subnet", JSON: "subnet", Computed: true, Replace: true, Desc: "Existing subnet of the VNet. KKP creates one when unset."},
			{Name: "route_table", JSON: "routeTable", Computed: true, Replace: true, Desc: "Existing route table of the subnet. KKP creates one when unset."},
			{Name: "security_group", JSON: "securityGroup", Computed: true, Replace: true, Desc: "Existing security group of the subnet. KKP creates one when unset."},
			{Name: "availability_set", JSON: "availabilitySet", Computed: true, Replace: true, Desc: "Existing availability set for the nodes."},
			{Name: "assign_availability_set", JSON: "assignAvailabilitySet", Kind: kBool, Replace: true, Desc: "Create and assign an availability set to the nodes."},
			{Name: "load_balancer_sku", JSON: "loadBalancerSKU", Computed: true, Replace: true, Desc: "Load balancer SKU: `basic` or `standard`."},
			{Name: "node_ports_allowed_ip_ranges", JSON: "nodePortsAllowedIPRanges", Kind: kRanges, Desc: "CIDRs allowed to reach NodePort services."},
		},
		CredentialSets: []credentialSet{{Required: []string{"tenant_id", "subscription_id", "client_id", "client_secret"}}},
	},
	{
		Name: "gcp", Title: "Google Cloud",
		Fields: []cloudField{
			{Name: "service_account", JSON: "serviceAccount", Kind: kBase64, Credential: true, Sensitive: true,
				Desc: "Service account key as JSON, e.g. `file(\"sa.json\")`. The provider base64-encodes it, as KKP expects."},
			{Name: "network", JSON: "network", Computed: true, Replace: true, Desc: "Existing VPC network, e.g. `global/networks/default`."},
			{Name: "subnetwork", JSON: "subnetwork", Computed: true, Replace: true, Desc: "Existing subnetwork, e.g. `projects/p/regions/r/subnetworks/s`."},
			{Name: "node_ports_allowed_ip_ranges", JSON: "nodePortsAllowedIPRanges", Kind: kRanges, Desc: "CIDRs allowed to reach NodePort services."},
		},
		CredentialSets: []credentialSet{{Required: []string{"service_account"}}},
	},
	{
		Name: "hetzner", Title: "Hetzner Cloud",
		Fields: []cloudField{
			{Name: "token", JSON: "token", Credential: true, Sensitive: true, Desc: "Hetzner Cloud API token."},
			{Name: "network", JSON: "network", Computed: true, Replace: true, Desc: "Existing Hetzner network. Defaults to the datacenter's network."},
		},
		CredentialSets: []credentialSet{{Required: []string{"token"}}},
	},
	{
		Name: "digitalocean", Title: "DigitalOcean",
		Fields: []cloudField{
			{Name: "token", JSON: "token", Credential: true, Sensitive: true, Desc: "DigitalOcean API token."},
		},
		CredentialSets: []credentialSet{{Required: []string{"token"}}},
	},
}

// Machine deployment node specs (spec.template.cloud.<provider>).
var nodeClouds = []cloudDef{
	{
		Name: "openstack", Title: "OpenStack",
		Fields: []cloudField{
			{Name: "flavor", JSON: "flavor", Required: true, Desc: "Flavor name."},
			{Name: "image", JSON: "image", Required: true, Desc: "Image name or ID. The datacenter's default images are listed by `kkp_datacenter`."},
			{Name: "disk_size", JSON: "diskSize", Kind: kInt, Desc: "Root volume size in GB. Unset uses the flavor's local disk."},
			{Name: "availability_zone", JSON: "availabilityZone", Desc: "Availability zone."},
			{Name: "server_group", JSON: "serverGroup", Desc: "Server group ID, e.g. for anti-affinity."},
			{Name: "use_floating_ip", JSON: "useFloatingIP", Kind: kBool, Default: b(false), Desc: "Give every node a floating IP."},
			{Name: "config_drive", JSON: "configDrive", Kind: kBool, Default: b(false), Desc: "Use a config drive instead of the metadata service."},
			{Name: "instance_ready_check_period", JSON: "instanceReadyCheckPeriod", Desc: "How often machine-controller checks a new instance, e.g. `5s`."},
			{Name: "instance_ready_check_timeout", JSON: "instanceReadyCheckTimeout", Desc: "How long machine-controller waits for a new instance, e.g. `2m`."},
			{Name: "tags", JSON: "tags", Kind: kStringMap, Desc: "OpenStack server tags (metadata)."},
		},
	},
	{
		Name: "aws", Title: "AWS",
		Fields: []cloudField{
			{Name: "instance_type", JSON: "instanceType", Required: true, Desc: "EC2 instance type, e.g. `t3.large`."},
			{Name: "disk_size", JSON: "diskSize", Kind: kInt, Required: true, Desc: "Root volume size in GB."},
			{Name: "volume_type", JSON: "volumeType", Required: true, Desc: "EBS volume type, e.g. `gp3`."},
			{Name: "ami", JSON: "ami", Computed: true, Desc: "AMI. Defaults to the datacenter's image for the operating system."},
			{Name: "availability_zone", JSON: "availabilityZone", Computed: true, Desc: "Availability zone; must match `subnet_id`."},
			{Name: "subnet_id", JSON: "subnetID", Computed: true, Desc: "Subnet of the cluster's VPC."},
			{Name: "assign_public_ip", JSON: "assignPublicIP", Kind: kBool, Default: b(true), Desc: "Give the nodes public IPs."},
			{Name: "ebs_volume_encrypted", JSON: "ebsVolumeEncrypted", Kind: kBool, Default: b(false), Desc: "Encrypt the root volume."},
			{Name: "is_spot_instance", JSON: "isSpotInstance", Kind: kBool, Default: b(false), Desc: "Use spot instances."},
			{Name: "spot_instance_max_price", JSON: "spotInstanceMaxPrice", Desc: "Maximum hourly spot price."},
			{Name: "spot_instance_persistent_request", JSON: "spotInstancePersistentRequest", Kind: kBool, Default: b(false), Desc: "Keep the spot request open after an interruption."},
			{Name: "spot_instance_interruption_behavior", JSON: "spotInstanceInterruptionBehavior", Desc: "`terminate`, `stop` or `hibernate`."},
			{Name: "tags", JSON: "tags", Kind: kStringMap, Desc: "Extra instance tags."},
		},
	},
	{
		Name: "azure", Title: "Azure",
		Fields: []cloudField{
			{Name: "size", JSON: "size", Required: true, Desc: "VM size, e.g. `Standard_D4s_v5`."},
			{Name: "image_id", JSON: "imageID", Computed: true, Desc: "Image ID. Defaults to the datacenter's image for the operating system."},
			{Name: "os_disk_size", JSON: "osDiskSize", Kind: kInt, Desc: "OS disk size in GB."},
			{Name: "data_disk_size", JSON: "dataDiskSize", Kind: kInt, Desc: "Data disk size in GB."},
			{Name: "zones", JSON: "zones", Kind: kStringList, Desc: "Availability zones, e.g. `[\"1\", \"2\"]`."},
			{Name: "assign_public_ip", JSON: "assignPublicIP", Kind: kBool, Default: b(false), Desc: "Give the nodes public IPs."},
			{Name: "assign_availability_set", JSON: "assignAvailabilitySet", Kind: kBool, Desc: "Put the nodes into the cluster's availability set."},
			{Name: "enable_accelerated_networking", JSON: "enableAcceleratedNetworking", Kind: kBool, Default: b(false), Desc: "Enable accelerated networking."},
			{Name: "tags", JSON: "tags", Kind: kStringMap, Desc: "Extra VM tags."},
		},
	},
	{
		Name: "gcp", Title: "Google Cloud",
		Fields: []cloudField{
			{Name: "machine_type", JSON: "machineType", Required: true, Desc: "Machine type, e.g. `e2-standard-4`."},
			{Name: "disk_size", JSON: "diskSize", Kind: kInt, Required: true, Desc: "Boot disk size in GB."},
			{Name: "disk_type", JSON: "diskType", Required: true, Desc: "Boot disk type, e.g. `pd-balanced`."},
			{Name: "zone", JSON: "zone", Computed: true, Desc: "Zone, e.g. `europe-west3-a`. Defaults to a zone of the datacenter."},
			{Name: "preemptible", JSON: "preemptible", Kind: kBool, Default: b(false), Desc: "Use preemptible VMs."},
			{Name: "custom_image", JSON: "customImage", Desc: "Custom image instead of the operating system's default."},
			{Name: "labels", JSON: "labels", Kind: kStringMap, Desc: "GCE labels."},
			{Name: "tags", JSON: "tags", Kind: kStringList, Desc: "GCE network tags."},
		},
	},
	{
		Name: "hetzner", Title: "Hetzner Cloud",
		Fields: []cloudField{
			{Name: "type", JSON: "type", Required: true, Desc: "Server type, e.g. `cx32`."},
			{Name: "network", JSON: "network", Computed: true, Desc: "Network. Defaults to the cluster's."},
		},
	},
	{
		Name: "digitalocean", Title: "DigitalOcean",
		Fields: []cloudField{
			{Name: "size", JSON: "size", Required: true, Desc: "Droplet size slug, e.g. `s-4vcpu-8gb`."},
			{Name: "backups", JSON: "backups", Kind: kBool, Default: b(false), Desc: "Enable droplet backups."},
			{Name: "monitoring", JSON: "monitoring", Kind: kBool, Default: b(false), Desc: "Enable droplet monitoring."},
			{Name: "tags", JSON: "tags", Kind: kStringList, Desc: "Extra droplet tags."},
		},
	},
}
