package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/SamuAlfageme/terraform-provider-kkp/internal/kkpmock"
)

// One cluster with a machine deployment per cloud provider besides OpenStack (covered in resource_cluster_test.go).
type cloudCase struct {
	name, dc string
	cluster  string // the cluster's cloud block
	node     string // the machine deployment's cloud block
	nodeEdit string // node block after an in-place change
	os       string
	// checks after create, as attribute → value ("~regexp" matches)
	checks map[string]string
	// credential attributes import cannot verify
	secrets []string
}

var cloudCases = []cloudCase{
	{
		name: "aws", dc: "aws-eu-central-1a", os: "amzn2",
		cluster:  `{ access_key_id = "AKIA0000", secret_access_key = "s3cret" }`,
		node:     `{ instance_type = "t3.large", disk_size = 50, volume_type = "gp3", tags = { team = "a" } }`,
		nodeEdit: `{ instance_type = "t3.xlarge", disk_size = 50, volume_type = "gp3", is_spot_instance = true }`,
		checks: map[string]string{
			"kkp_cluster.c:aws.vpc_id": "vpc-0a1b2c3d", "kkp_cluster.c:aws.security_group_id": "~^sg-",
			"kkp_cluster.c:aws.secret_access_key": "s3cret", "kkp_cluster.c:aws.disable_iam_reconciling": "false",
			"kkp_machine_deployment.m:aws.ami": "ami-0amzn2", "kkp_machine_deployment.m:aws.availability_zone": "eu-central-1a",
			"kkp_machine_deployment.m:aws.assign_public_ip": "true",
		},
		secrets: []string{"aws.access_key_id", "aws.secret_access_key"},
	},
	{
		name: "azure", dc: "azure-westeurope", os: "ubuntu",
		cluster:  `{ tenant_id = "t", subscription_id = "s", client_id = "c", client_secret = "s3cret" }`,
		node:     `{ size = "Standard_D4s_v5", zones = ["1", "2"] }`,
		nodeEdit: `{ size = "Standard_D8s_v5", os_disk_size = 100 }`,
		checks: map[string]string{
			"kkp_cluster.c:azure.resource_group": "~^cluster-", "kkp_cluster.c:azure.load_balancer_sku": "standard",
			"kkp_machine_deployment.m:azure.image_id": "/subscriptions/0000/images/ubuntu-2404",
			"kkp_machine_deployment.m:azure.zones.#":  "2",
		},
		secrets: []string{"azure.tenant_id", "azure.subscription_id", "azure.client_id", "azure.client_secret"},
	},
	{
		name: "gcp", dc: "gcp-europe-west3", os: "ubuntu",
		cluster:  `{ service_account = jsonencode({ type = "service_account", project_id = "p" }) }`,
		node:     `{ machine_type = "e2-standard-4", disk_size = 50, disk_type = "pd-balanced", tags = ["workers"] }`,
		nodeEdit: `{ machine_type = "e2-standard-8", disk_size = 50, disk_type = "pd-balanced", preemptible = true, labels = { team = "a" } }`,
		checks: map[string]string{
			"kkp_cluster.c:gcp.network":                 "global/networks/default",
			"kkp_machine_deployment.m:gcp.zone":         "europe-west3-a",
			"kkp_machine_deployment.m:gcp.preemptible":  "false",
			"kkp_machine_deployment.m:gcp.tags.0":       "workers",
			"kkp_cluster.c:gcp.service_account":         "~service_account",
			"kkp_cluster.c:gcp.subnetwork":              "",
			"kkp_machine_deployment.m:operating_system": "ubuntu",
		},
		secrets: []string{"gcp.service_account"},
	},
	{
		name: "hetzner", dc: "hetzner-nbg1", os: "ubuntu",
		cluster:  `{ token = "hcloud-token" }`,
		node:     `{ type = "cx32" }`,
		nodeEdit: `{ type = "cx42" }`,
		checks: map[string]string{
			"kkp_cluster.c:hetzner.network": "kubermatic", "kkp_machine_deployment.m:hetzner.network": "kubermatic",
		},
		secrets: []string{"hetzner.token"},
	},
	{
		name: "digitalocean", dc: "do-ams3", os: "rockylinux",
		cluster:  `{ token = "do-token" }`,
		node:     `{ size = "s-4vcpu-8gb", tags = ["kkp"] }`,
		nodeEdit: `{ size = "s-8vcpu-16gb", monitoring = true }`,
		checks: map[string]string{
			"kkp_machine_deployment.m:digitalocean.backups": "false", "kkp_machine_deployment.m:digitalocean.tags.#": "1",
		},
		secrets: []string{"digitalocean.token"},
	},
}

func cloudConfig(tc cloudCase, cluster, node string) string {
	return fmt.Sprintf(`
resource "kkp_cluster" "c" {
  name       = %[1]q
  datacenter = %[2]q
  version    = "1.32.5"
  %[1]s = %[3]s
}

resource "kkp_machine_deployment" "m" {
  cluster_id       = kkp_cluster.c.id
  name             = "workers"
  replicas         = 2
  operating_system = %[4]q
  %[1]s = %[5]s
}
`, tc.name, tc.dc, cluster, tc.os, node)
}

func checksFor(m map[string]string) resource.TestCheckFunc {
	var fs []resource.TestCheckFunc
	for k, v := range m {
		res, attr, _ := strings.Cut(k, ":")
		switch {
		case v == "":
			fs = append(fs, resource.TestCheckNoResourceAttr(res, attr))
		case strings.HasPrefix(v, "~"):
			fs = append(fs, resource.TestMatchResourceAttr(res, attr, regexp.MustCompile(v[1:])))
		default:
			fs = append(fs, resource.TestCheckResourceAttr(res, attr, v))
		}
	}
	return resource.ComposeAggregateTestCheckFunc(fs...)
}

func TestAccClouds(t *testing.T) {
	for _, tc := range cloudCases {
		t.Run(tc.name, func(t *testing.T) {
			s := newMock(t)
			project := s.AddProject("platform")
			cfg := providerConfig(s, s.ServiceAccountToken(project), "")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: protoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: cfg + cloudConfig(tc, tc.cluster, tc.node), Check: checksFor(tc.checks)},
					{
						// Changing the machine type rolls the nodes in place.
						Config: cfg + cloudConfig(tc, tc.cluster, tc.nodeEdit),
						Check:  resource.TestCheckResourceAttr("kkp_machine_deployment.m", "available_replicas", "2"),
					},
					{
						ResourceName: "kkp_cluster.c", ImportState: true, ImportStateVerify: true,
						ImportStateIdFunc:       importID("kkp_cluster.c", "project_id", "id"),
						ImportStateVerifyIgnore: append([]string{"timeouts"}, tc.secrets...),
					},
					{
						ResourceName: "kkp_machine_deployment.m", ImportState: true, ImportStateVerify: true,
						ImportStateIdFunc:       importID("kkp_machine_deployment.m", "project_id", "cluster_id", "id"),
						ImportStateVerifyIgnore: []string{"timeouts"},
					},
				},
			})
		})
	}
}

func TestAccCloudPresetsAndMismatches(t *testing.T) {
	s := newMock(t)
	cfg := providerConfig(s, kkpmock.AdminToken, s.AddProject("platform"))
	cluster := func(dc, block string) string {
		return cfg + fmt.Sprintf(`
resource "kkp_cluster" "c" {
  name       = "x"
  datacenter = %q
  version    = "1.32.5"
  %s
}`, dc, block)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cluster("do-ams3", ""), PlanOnly: true, ExpectError: regexp.MustCompile(`Exactly one cloud provider block\s+required`)},
			{Config: cluster("do-ams3", "hetzner = { token = \"a\" }\ndigitalocean = { token = \"b\" }"), PlanOnly: true,
				ExpectError: regexp.MustCompile(`Exactly one cloud provider block\s+required`)},
			{Config: cluster("aws-eu-central-1a", `aws = { access_key_id = "a" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`Incomplete credentials`)},
			{Config: cluster("azure-westeurope", `azure = { tenant_id = "a" }`), PlanOnly: true, ExpectError: regexp.MustCompile(`Incomplete credentials`)},
			{Config: cluster("hetzner-nbg1", `hetzner = {}`), PlanOnly: true, ExpectError: regexp.MustCompile(`Missing credentials`)},
			// The block has to match the datacenter's provider; KKP says so.
			{Config: cluster("hetzner-nbg1", `digitalocean = { token = "a" }`), ExpectError: regexp.MustCompile(`spec.cloud.hetzner is\s+not set`)},
			// A preset of the matching provider with an empty block.
			{
				Config: cluster("aws-eu-central-1a", "credential_preset = \"team-aws\"\naws = {}"),
				Check:  resource.TestCheckResourceAttr("kkp_cluster.c", "aws.vpc_id", "vpc-0a1b2c3d"),
			},
			{
				// The machine deployment block has to match the cluster's provider.
				Config: cluster("aws-eu-central-1a", "credential_preset = \"team-aws\"\naws = {}") + `
resource "kkp_machine_deployment" "m" {
  cluster_id       = kkp_cluster.c.id
  name             = "workers"
  replicas         = 1
  operating_system = "ubuntu"
  gcp = { machine_type = "e2-small", disk_size = 20, disk_type = "pd-standard" }
}`,
				ExpectError: regexp.MustCompile(`must be for the cluster's\s+cloud\s+provider,\s+aws`),
			},
		},
	})
}
