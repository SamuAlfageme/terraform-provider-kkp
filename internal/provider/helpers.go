package provider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
)

// pollInterval is how often the provider polls KKP while waiting; tests shorten it.
var pollInterval = 5 * time.Second

// base is embedded by every resource and data source.
type base struct {
	data *providerData
}

func (b *base) configure(raw any, diags *diag.Diagnostics) {
	if raw == nil {
		return
	}
	d, ok := raw.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", raw))
		return
	}
	b.data = d
}

func (b *base) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	b.configure(req.ProviderData, &resp.Diagnostics)
}

type dsBase struct{ base }

func (b *dsBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	b.configure(req.ProviderData, &resp.Diagnostics)
}

func (b *base) client() *client.Client { return b.data.client }

// project returns the configured project ID, falling back to the provider default.
func (b *base) project(v types.String, diags *diag.Diagnostics) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	if b.data != nil && b.data.defaultProject != "" {
		return b.data.defaultProject
	}
	diags.AddError("Missing project_id",
		"Set project_id on the resource or the provider, set KKP_PROJECT_ID, or use a project service account token, which names its project.")
	return ""
}

// waitFor polls check until it reports done, an error, or ctx ends.
func waitFor(ctx context.Context, what string, check func() (bool, error)) error {
	var last error
	for {
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("timed out waiting for %s: %w", what, last)
			}
			return fmt.Errorf("timed out waiting for %s", what)
		case <-time.After(pollInterval):
		}
	}
}

// waitGone polls get until KKP answers 404.
func waitGone(ctx context.Context, what string, get func() error) error {
	return waitFor(ctx, what+" to be deleted", func() (bool, error) {
		err := get()
		if client.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}

func addAPIError(diags *diag.Diagnostics, summary string, err error) {
	detail := err.Error()
	switch {
	case client.IsForbidden(err):
		detail += "\n\nKKP refused the request. Project service account tokens can only manage their own project, " +
			"and cannot create or delete projects; viewers cannot change anything."
	case errors.Is(err, context.DeadlineExceeded):
		detail += "\n\nIncrease the timeouts block of the resource if KKP is slow to reconcile."
	}
	diags.AddError(summary, detail)
}

// splitID splits an import ID like "project/cluster".
func splitID(id string, n int, format string) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != n || slices.Contains(parts, "") {
		return nil, fmt.Errorf("expected import ID in the form %q, got %q", format, id)
	}
	return parts, nil
}

// Conversions between API values and framework types.

func strOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func mapValue(m map[string]string) types.Map {
	if len(m) == 0 {
		return types.MapNull(types.StringType)
	}
	vals := make(map[string]attr.Value, len(m))
	for k, v := range m {
		vals[k] = types.StringValue(v)
	}
	return types.MapValueMust(types.StringType, vals)
}

// mapKeep returns m as a Map, but keeps an empty map from prior state as empty (not null) so that
// `labels = {}` does not show a diff on every plan.
func mapKeep(m map[string]string, prior types.Map) types.Map {
	if len(m) == 0 && !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
		return prior
	}
	return mapValue(m)
}

func goMap(ctx context.Context, m types.Map, diags *diag.Diagnostics) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &out, false)...)
	return out
}

func goStrings(ctx context.Context, v interface {
	IsNull() bool
	IsUnknown() bool
	ElementsAs(context.Context, any, bool) diag.Diagnostics
}, diags *diag.Diagnostics) []string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	return out
}

func listValue(s []string, prior types.List) types.List {
	if len(s) == 0 {
		if !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
			return prior
		}
		return types.ListNull(types.StringType)
	}
	vals := make([]attr.Value, len(s))
	for i, v := range s {
		vals[i] = types.StringValue(v)
	}
	return types.ListValueMust(types.StringType, vals)
}

func setValue(s []string, prior types.Set) types.Set {
	if len(s) == 0 {
		if !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
			return prior
		}
		return types.SetNull(types.StringType)
	}
	vals := make([]attr.Value, len(s))
	for i, v := range s {
		vals[i] = types.StringValue(v)
	}
	return types.SetValueMust(types.StringType, vals)
}

// mapPatch is the JSON merge patch that turns old into want: changed keys with their value, removed keys as null.
func mapPatch(old, want map[string]string) map[string]any {
	out := map[string]any{}
	for k := range old {
		if _, ok := want[k]; !ok {
			out[k] = nil
		}
	}
	for k, v := range want {
		if ov, ok := old[k]; !ok || ov != v {
			out[k] = v
		}
	}
	return out
}

// withoutKeys returns m without the given keys (nil when nothing is left).
func withoutKeys(m map[string]string, keys ...string) map[string]string {
	out := maps.Clone(m)
	for _, k := range keys {
		delete(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// groupRole strips the "-<project id>" suffix KKP adds to group names.
func groupRole(group, projectID string) string {
	return strings.TrimSuffix(group, "-"+projectID)
}

func ptr[T any](v T) *T { return &v }

type diagsT = diag.Diagnostics
