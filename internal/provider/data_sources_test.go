package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

func TestAccDataSources(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	cfg := providerConfig(s, s.ServiceAccountToken(project), "") + `
data "kkp_datacenters" "openstack" {
  cloud_provider = "openstack"
}

data "kkp_datacenter" "zurich" {
  name = data.kkp_datacenters.openstack.names[1]
}

data "kkp_kubernetes_versions" "os" {}

data "kkp_project" "mine" {}

data "kkp_projects" "all" {}

resource "kkp_cluster" "c" {
  name              = "lookup-me"
  datacenter        = data.kkp_datacenter.zurich.name
  version           = data.kkp_kubernetes_versions.os.default_version
  credential_preset = "shared-openstack"
  openstack         = {}
}

data "kkp_cluster" "by_name" {
  name       = "lookup-me"
  depends_on = [kkp_cluster.c]
}

data "kkp_cluster_kubeconfig" "c" {
  cluster_id = kkp_cluster.c.id
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.kkp_datacenters.openstack", "names.#", "2"),
				resource.TestCheckResourceAttr("data.kkp_datacenter.zurich", "name", "os-zurich"),
				resource.TestCheckResourceAttr("data.kkp_datacenter.zurich", "openstack_region", "zrh"),
				resource.TestCheckResourceAttr("data.kkp_datacenter.zurich", "images.ubuntu", "Ubuntu 24.04"),
				resource.TestCheckResourceAttr("data.kkp_kubernetes_versions.os", "default_version", kkpmock.Versions[1]),
				resource.TestCheckResourceAttr("data.kkp_kubernetes_versions.os", "latest_version", kkpmock.Versions[2]),
				resource.TestCheckResourceAttr("data.kkp_project.mine", "id", project),
				resource.TestCheckResourceAttr("data.kkp_project.mine", "status", "Active"),
				resource.TestCheckResourceAttr("data.kkp_projects.all", "projects.#", "1"),
				resource.TestCheckResourceAttrPair("data.kkp_cluster.by_name", "id", "kkp_cluster.c", "id"),
				resource.TestCheckResourceAttr("data.kkp_cluster.by_name", "ready", "true"),
				resource.TestCheckResourceAttr("data.kkp_cluster.by_name", "available_upgrades.0", kkpmock.Versions[2]),
				resource.TestMatchResourceAttr("data.kkp_cluster_kubeconfig.c", "kubeconfig", regexp.MustCompile(`server: https://`)),
			),
		}},
	})
}

func TestAccProviderConfigErrors(t *testing.T) {
	s := newMock(t)
	t.Setenv("KKP_HOST", "")
	t.Setenv("KKP_TOKEN", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      `data "kkp_projects" "all" {}`,
				ExpectError: regexp.MustCompile(`Missing KKP host`),
			},
			{
				Config:      providerConfig(s, "wrong-token", "") + `data "kkp_projects" "all" {}`,
				ExpectError: regexp.MustCompile(`401 invalid token`),
			},
			{
				// Without a project anywhere, project-scoped resources say where to set one.
				Config:      providerConfig(s, kkpmock.AdminToken, "") + `data "kkp_project" "p" {}`,
				ExpectError: regexp.MustCompile(`Missing project_id`),
			},
		},
	})
}
