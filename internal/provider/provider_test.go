package provider

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/client"
	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

// The provider tests drive OpenTofu or Terraform against the in-memory KKP in internal/kkpmock, so they need
// no credentials. `make test` runs them through OpenTofu, `make test-terraform` through Terraform.
var protoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"kkp": providerserver.NewProtocol6WithError(New("test")()),
}

func TestMain(m *testing.M) {
	pollInterval = 10 * time.Millisecond
	testClientHook = func(c *client.Client) { c.SetRetryWait(time.Millisecond) }
	os.Exit(m.Run())
}

func newMock(t *testing.T) *kkpmock.Server {
	t.Helper()
	s := kkpmock.New()
	t.Cleanup(s.Close)
	return s
}

// providerConfig points the provider at the mock. An empty project leaves project_id unset.
func providerConfig(s *kkpmock.Server, token, project string) string {
	p := ""
	if project != "" {
		p = fmt.Sprintf("project_id = %q", project)
	}
	return fmt.Sprintf(`
provider "kkp" {
  host  = %q
  token = %q
  %s
}
`, s.URL, token, p)
}
