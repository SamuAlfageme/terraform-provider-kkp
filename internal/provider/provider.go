// Package provider implements the KKP OpenTofu/Terraform provider.
package provider

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
)

var _ provider.Provider = (*kkpProvider)(nil)

// New returns the provider factory for a given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &kkpProvider{version: version} }
}

type kkpProvider struct {
	version string
}

type providerModel struct {
	Host               types.String `tfsdk:"host"`
	Token              types.String `tfsdk:"token"`
	TokenFile          types.String `tfsdk:"token_file"`
	ProjectID          types.String `tfsdk:"project_id"`
	CACertificate      types.String `tfsdk:"ca_certificate"`
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	MaxRetries         types.Int64  `tfsdk:"max_retries"`
}

// providerData is handed to every resource and data source.
type providerData struct {
	client *client.Client
	// defaultProject is used when a resource leaves project_id unset.
	defaultProject string
}

func (p *kkpProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "kkp"
	resp.Version = p.version
}

func (p *kkpProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages projects, clusters, machine deployments and access in a " +
			"[Kubermatic Kubernetes Platform](https://www.kubermatic.com/products/kubermatic-kubernetes-platform/) (KKP) " +
			"installation through its REST API, authenticating with a bearer token (normally a project service account token).",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Base URL of the KKP dashboard/API, e.g. `https://kkp.example.com`. Defaults to the `KKP_HOST` environment variable.",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Bearer token for the KKP API. Normally a project service account token (Project → Service Accounts → Add token). " +
					"Defaults to `KKP_TOKEN`. Conflicts with `token_file`.",
			},
			"token_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to a file holding the token. Defaults to `KKP_TOKEN_FILE`.",
			},
			"project_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Project used by resources and data sources that leave `project_id` unset. Defaults to `KKP_PROJECT_ID`, " +
					"then to the project a service account token belongs to (read from the token).",
			},
			"ca_certificate": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "PEM CA bundle to trust in addition to the system roots. Defaults to `KKP_CA_CERTIFICATE`.",
			},
			"insecure_skip_verify": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Skip TLS verification. Only for lab installations. Defaults to `KKP_INSECURE_SKIP_VERIFY`.",
			},
			"max_retries": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "How often a request is retried on rate limiting, gateway errors and network errors. Defaults to 4.",
			},
		},
	}
}

func envOr(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *kkpProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for name, v := range map[string]types.String{"host": cfg.Host, "token": cfg.Token, "token_file": cfg.TokenFile} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown "+name,
				"The provider cannot be configured with a value that is only known after apply. Set it statically or through the environment.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	host := envOr(cfg.Host, "KKP_HOST")
	token := envOr(cfg.Token, "KKP_TOKEN")
	tokenFile := envOr(cfg.TokenFile, "KKP_TOKEN_FILE")
	if !cfg.Token.IsNull() && !cfg.TokenFile.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("token_file"), "Conflicting token settings", "Set either token or token_file, not both.")
		return
	}
	if token == "" && tokenFile != "" {
		raw, err := os.ReadFile(tokenFile)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("token_file"), "Cannot read token file", err.Error())
			return
		}
		token = strings.TrimSpace(string(raw))
	}
	if host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("host"), "Missing KKP host", "Set host in the provider block or the KKP_HOST environment variable.")
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"), "Missing KKP token",
			"Set token or token_file in the provider block, or the KKP_TOKEN / KKP_TOKEN_FILE environment variables.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	insecure := cfg.InsecureSkipVerify.ValueBool()
	if cfg.InsecureSkipVerify.IsNull() {
		insecure, _ = strconv.ParseBool(os.Getenv("KKP_INSECURE_SKIP_VERIFY"))
	}
	retries := 4
	if !cfg.MaxRetries.IsNull() {
		retries = int(cfg.MaxRetries.ValueInt64())
	}
	c, err := client.New(client.Config{
		Host:               host,
		Token:              token,
		CACertPEM:          envOr(cfg.CACertificate, "KKP_CA_CERTIFICATE"),
		InsecureSkipVerify: insecure,
		MaxRetries:         retries,
		UserAgent:          "terraform-provider-kkp/" + p.version,
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid KKP provider configuration", err.Error())
		return
	}
	if testClientHook != nil {
		testClientHook(c)
	}

	project := envOr(cfg.ProjectID, "KKP_PROJECT_ID")
	if project == "" {
		project = c.TokenProjectID()
	}
	data := &providerData{client: c, defaultProject: project}
	resp.ResourceData = data
	resp.DataSourceData = data
}

// testClientHook lets tests tweak the client (shorter retry backoff).
var testClientHook func(*client.Client)

func (p *kkpProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newProjectResource,
		newSSHKeyResource,
		newServiceAccountResource,
		newServiceAccountTokenResource,
		newProjectMemberResource,
		newClusterResource,
		newMachineDeploymentResource,
		newClusterServiceAccountResource,
	}
}

func (p *kkpProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newProjectDataSource,
		newProjectsDataSource,
		newDatacenterDataSource,
		newDatacentersDataSource,
		newKubernetesVersionsDataSource,
		newClusterDataSource,
		newClusterKubeconfigDataSource,
	}
}
