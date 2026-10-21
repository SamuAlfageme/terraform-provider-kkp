package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

func clusterConfig(version, labels, sshKeys, extra string) string {
	return fmt.Sprintf(`
resource "kkp_ssh_key" "a" {
  name       = "a"
  public_key = %q
}

resource "kkp_ssh_key" "b" {
  name       = "b"
  public_key = %q
}

resource "kkp_cluster" "c" {
  name       = "staging"
  datacenter = "os-zurich"
  version    = %q
  labels     = %s
  ssh_key_ids = %s

  openstack = {
    username = "svc-kkp"
    password = "s3cret"
    domain   = "Default"
    project  = "kkp-staging"
  }

  delete_volumes_on_destroy = true
  %s
}
`, testPublicKey(1), testPublicKey(2), version, labels, sshKeys, extra)
}

func TestAccClusterLifecycle(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	cfg := providerConfig(s, s.ServiceAccountToken(project), "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if s.LastDelete.DeleteVolumes != "true" || s.LastDelete.DeleteLoadBalancers != "false" {
				return fmt.Errorf("unexpected cleanup headers %+v", s.LastDelete)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg + clusterConfig("1.32.5", `{ env = "staging" }`, `[kkp_ssh_key.a.id]`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_cluster.c", "project_id", project),
					resource.TestCheckResourceAttr("kkp_cluster.c", "version", "1.32.5"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "cni_type", "cilium"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "proxy_mode", "ebpf"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "pods_cidr", "172.25.0.0/16"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "expose_strategy", "NodePort"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "ssh_key_ids.#", "1"),
					// KKP creates the network once the cluster is up; the provider waited for that.
					resource.TestCheckResourceAttr("kkp_cluster.c", "openstack.floating_ip_pool", "public"),
					resource.TestMatchResourceAttr("kkp_cluster.c", "openstack.network", regexp.MustCompile(`^kubernetes-`)),
					// Credentials are never returned by KKP; state keeps the configured ones.
					resource.TestCheckResourceAttr("kkp_cluster.c", "openstack.password", "s3cret"),
					resource.TestMatchResourceAttr("kkp_cluster.c", "url", regexp.MustCompile(`^https://`)),
				),
			},
			{
				// Upgrade, relabel, swap SSH keys, enable audit logging and an admission plugin in place.
				Config: cfg + clusterConfig("1.33.1", `{ env = "staging", tier = "gold" }`, `[kkp_ssh_key.b.id]`, `
  audit_logging       = true
  audit_policy_preset = "recommended"
  admission_plugins   = ["PodNodeSelector"]
  konnectivity        = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_cluster.c", "version", "1.33.1"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "labels.tier", "gold"),
					resource.TestCheckResourceAttrPair("kkp_cluster.c", "ssh_key_ids.0", "kkp_ssh_key.b", "id"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "audit_logging", "true"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "konnectivity", "false"),
				),
			},
			{
				// Removing a label sends null for it in the merge patch.
				Config: cfg + clusterConfig("1.33.1", `{ tier = "gold" }`, `[]`, `
  audit_logging       = true
  audit_policy_preset = "recommended"
  admission_plugins   = ["PodNodeSelector"]
  konnectivity        = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_cluster.c", "labels.%", "1"),
					resource.TestCheckResourceAttr("kkp_cluster.c", "ssh_key_ids.#", "0"),
				),
			},
			{
				ResourceName:      "kkp_cluster.c",
				ImportState:       true,
				ImportStateIdFunc: importID("kkp_cluster.c", "project_id", "id"),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"openstack.username", "openstack.password", "openstack.domain", "openstack.project",
					"delete_volumes_on_destroy", "timeouts", "ssh_key_ids"},
			},
		},
	})
}

func TestAccClusterRejectedChanges(t *testing.T) {
	s := newMock(t)
	project := s.AddProject("platform")
	cfg := providerConfig(s, kkpmock.AdminToken, project)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg + clusterConfig("1.31.9", "{}", "[]", "")},
			{
				// KKP only upgrades one minor version at a time.
				Config:      cfg + clusterConfig("1.33.1", "{}", "[]", ""),
				ExpectError: regexp.MustCompile(`one minor version at a time`),
			},
			{
				// The datacenter cannot change in place, so the plan replaces the cluster.
				Config: cfg + strings.Replace(clusterConfig("1.31.9", "{}", "[]", ""), "os-zurich", "os-lausanne", 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("kkp_cluster.c", plancheck.ResourceActionDestroyBeforeCreate),
				}},
			},
		},
	})
}

func TestAccClusterValidation(t *testing.T) {
	s := newMock(t)
	cfg := providerConfig(s, kkpmock.AdminToken, s.AddProject("platform"))
	cluster := func(openstack, extra string) string {
		return cfg + fmt.Sprintf(`
resource "kkp_cluster" "c" {
  name       = "x"
  datacenter = "os-zurich"
  version    = "1.32.5"
  openstack  = %s
  %s
}`, openstack, extra)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cluster(`{}`, ""), PlanOnly: true, ExpectError: regexp.MustCompile(`Missing credentials`)},
			{Config: cluster(`{ username = "u", password = "p" }`, ""), PlanOnly: true, ExpectError: regexp.MustCompile(`Incomplete credentials`)},
			{Config: cluster(`{ application_credential_id = "id" }`, ""), PlanOnly: true, ExpectError: regexp.MustCompile(`Incomplete credentials`)},
			{Config: cluster(`{ username = "u" }`, `credential_preset = "p"`), PlanOnly: true, ExpectError: regexp.MustCompile(`Conflicting credentials`)},
			{Config: cluster(`{}`, `expose_strategy = "Magic"`), PlanOnly: true, ExpectError: regexp.MustCompile(`value must be one of`)},
		},
	})
}

func TestAccClusterPresetAndMachineDeployment(t *testing.T) {
	s := newMock(t)
	s.HealthPolls = 5
	project := s.AddProject("platform")
	cfg := providerConfig(s, s.ServiceAccountToken(project), "")
	md := func(replicas int, extra string) string {
		return cfg + fmt.Sprintf(`
resource "kkp_cluster" "c" {
  name              = "edge"
  datacenter        = "os-zurich"
  version           = "1.32.5"
  credential_preset = "switch-openstack"
  openstack         = {}
  # Do not wait: the machine deployment has to retry until KKP accepts it.
  wait_for_ready = false
}

resource "kkp_machine_deployment" "workers" {
  cluster_id       = kkp_cluster.c.id
  name             = "workers"
  replicas         = %d
  operating_system = "ubuntu"
  node_labels      = { pool = "general" }
  openstack = {
    flavor    = "m1.large"
    image     = "Ubuntu 24.04"
    disk_size = 50
    tags      = { owner = "platform" }
  }
  %s
}
`, replicas, extra)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: md(2, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "id", "workers"),
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "kubelet_version", "1.32.5"),
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "available_replicas", "2"),
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "openstack.disk_size", "50"),
					func(*terraform.State) error {
						posts := 0
						for _, r := range s.Requests() {
							if strings.HasPrefix(r, "POST ") && strings.HasSuffix(r, "/machinedeployments") {
								posts++
							}
						}
						if posts < 2 {
							return fmt.Errorf("expected the create to be retried while the cluster was not ready, got %d POSTs", posts)
						}
						return nil
					},
				),
			},
			{
				// Scale, autoscale, switch to Flatcar, drop the disk size and a tag, add a taint and an OSP.
				Config: md(3, `
  min_replicas                = 1
  max_replicas                = 5
  operating_system_profile    = "osp-flatcar-custom"
  taints = [{ key = "dedicated", value = "gpu", effect = "NoSchedule" }]
`) + "",
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "replicas", "3"),
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "max_replicas", "5"),
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "taints.0.effect", "NoSchedule"),
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "operating_system_profile", "osp-flatcar-custom"),
				),
			},
			{
				Config: strings.NewReplacer(`operating_system = "ubuntu"`, `operating_system = "flatcar"`, "disk_size = 50", "", `tags      = { owner = "platform" }`, "").
					Replace(md(3, "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kkp_machine_deployment.workers", "operating_system", "flatcar"),
					resource.TestCheckNoResourceAttr("kkp_machine_deployment.workers", "openstack.disk_size"),
					resource.TestCheckNoResourceAttr("kkp_machine_deployment.workers", "min_replicas"),
					resource.TestCheckNoResourceAttr("kkp_machine_deployment.workers", "openstack.tags"),
					resource.TestCheckNoResourceAttr("kkp_machine_deployment.workers", "operating_system_profile"),
				),
			},
			{
				// A kubelet newer than the control plane is refused by KKP.
				Config:      strings.Replace(md(3, `kubelet_version = "1.33.1"`), `operating_system = "ubuntu"`, `operating_system = "flatcar"`, 1),
				ExpectError: regexp.MustCompile(`cannot be newer than the control plane`),
			},
			{
				ResourceName:            "kkp_machine_deployment.workers",
				ImportState:             true,
				ImportStateIdFunc:       importID("kkp_machine_deployment.workers", "project_id", "cluster_id", "id"),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
		},
	})
}
