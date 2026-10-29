package provider

import (
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

func clusterSAConfig(roles string) string {
	return fmt.Sprintf(`
resource "kkp_cluster" "c" {
  name       = "canary"
  datacenter = "os-zurich"
  version    = "1.32.5"

  openstack = {
    username = "svc-kkp"
    password = "s3cret"
    domain   = "Default"
    project  = "kkp-staging"
  }
}

resource "kkp_cluster_service_account" "sa" {
  cluster_id    = kkp_cluster.c.id
  name          = "canary"
  cluster_roles = %s
}
`, roles)
}

// checkClusterSARoles compares the cluster roles the fake KKP has bound the service account to.
func checkClusterSARoles(s *kkpmock.Server, want ...string) resource.TestCheckFunc {
	return func(st *terraform.State) error {
		r := st.RootModule().Resources["kkp_cluster_service_account.sa"].Primary.Attributes
		got := s.ClusterServiceAccountRoles(r["project_id"], r["cluster_id"], r["namespace"], r["name"])
		if !slices.Equal(got, want) {
			return fmt.Errorf("bound to %v, want %v", got, want)
		}
		return nil
	}
}

func TestAccClusterServiceAccount(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	cfg := providerConfig(s, s.ServiceAccountToken(project), "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, req := range s.Requests() {
				if regexp.MustCompile(`^DELETE /api/v2/projects/[^/]+/clusters/[^/]+/serviceaccount/kube-system/canary$`).MatchString(req) {
					return nil
				}
			}
			return fmt.Errorf("the cluster service account was not deleted")
		},
		Steps: []resource.TestStep{
			{
				Config: cfg + clusterSAConfig(`["cluster-admin"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_cluster_service_account.sa", "id", "kube-system/canary"),
					resource.TestCheckResourceAttr("kkp_cluster_service_account.sa", "namespace", "kube-system"),
					resource.TestCheckResourceAttr("kkp_cluster_service_account.sa", "project_id", project),
					resource.TestCheckResourceAttr("kkp_cluster_service_account.sa", "cluster_roles.#", "1"),
					resource.TestCheckResourceAttrSet("kkp_cluster_service_account.sa", "creation_timestamp"),
					// The first kubeconfig request finds the token secret still empty; the provider waited.
					resource.TestMatchResourceAttr("kkp_cluster_service_account.sa", "kubeconfig", regexp.MustCompile(`token: mock-sa-token\.kube-system\.canary`)),
					checkClusterSARoles(s, "cluster-admin"),
				),
			},
			{
				// Changing the roles rebinds in place and keeps the token.
				Config: cfg + clusterSAConfig(`["view", "edit"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("kkp_cluster_service_account.sa", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_cluster_service_account.sa", "cluster_roles.#", "2"),
					checkClusterSARoles(s, "edit", "view"),
				),
			},
			{
				ResourceName:      "kkp_cluster_service_account.sa",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importID("kkp_cluster_service_account.sa", "project_id", "cluster_id", "namespace", "name"),
			},
			{
				// KKP only keeps bindings for cluster-admin, edit, view and labelled cluster roles.
				Config:      cfg + clusterSAConfig(`["view", "edit", "admin"]`),
				ExpectError: regexp.MustCompile(`the cluster role binding not found`),
			},
			{
				// The failed apply bound nothing; dropping the roles unbinds the rest.
				Config: cfg + clusterSAConfig(`null`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("kkp_cluster_service_account.sa", "cluster_roles"),
					checkClusterSARoles(s),
				),
			},
		},
	})
}
