# OpenTofu / Terraform Provider for KKP

[![Tests](https://github.com/SamuAlfageme/terraform-provider-kkp/actions/workflows/test.yml/badge.svg)](https://github.com/SamuAlfageme/terraform-provider-kkp/actions/workflows/test.yml)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL_2.0-brightgreen.svg)](LICENSE)

Manage [Kubermatic Kubernetes Platform](https://www.kubermatic.com/products/kubermatic-kubernetes-platform/) (KKP) as code: projects, clusters, machine deployments, SSH keys, service accounts and members, through the KKP REST API and a project service account token.

```hcl
data "kkp_kubernetes_versions" "openstack" {}

resource "kkp_cluster" "staging" {
  name              = "staging"
  datacenter        = "os-zurich"
  version           = data.kkp_kubernetes_versions.openstack.default_version
  credential_preset = "openstack"
  openstack         = {}
}

resource "kkp_machine_deployment" "workers" {
  cluster_id       = kkp_cluster.staging.id
  name             = "workers"
  replicas         = 3
  operating_system = "ubuntu"
  openstack = {
    flavor = "m1.large"
    image  = "Ubuntu 24.04"
  }
}
```

## Features

| Object | Resource | Data source |
|---|---|---|
| Projects | [`kkp_project`](docs/resources/project.md) | [`kkp_project`](docs/data-sources/project.md), [`kkp_projects`](docs/data-sources/projects.md) |
| Clusters | [`kkp_cluster`](docs/resources/cluster.md) | [`kkp_cluster`](docs/data-sources/cluster.md), [`kkp_cluster_kubeconfig`](docs/data-sources/cluster_kubeconfig.md) |
| Machine deployments | [`kkp_machine_deployment`](docs/resources/machine_deployment.md) | – |
| SSH keys | [`kkp_ssh_key`](docs/resources/ssh_key.md) | – |
| Service accounts and tokens | [`kkp_service_account`](docs/resources/service_account.md), [`kkp_service_account_token`](docs/resources/service_account_token.md) | – |
| Service accounts in user clusters | [`kkp_cluster_service_account`](docs/resources/cluster_service_account.md) | – |
| Project members | [`kkp_project_member`](docs/resources/project_member.md) | – |
| Datacenters, versions | – | [`kkp_datacenter`](docs/data-sources/datacenter.md), [`kkp_datacenters`](docs/data-sources/datacenters.md), [`kkp_kubernetes_versions`](docs/data-sources/kubernetes_versions.md) |

* Clusters and machine deployments on OpenStack, AWS, Azure, Google Cloud, Hetzner and DigitalOcean (see [Compatibility](#compatibility))
* Bearer token authentication; the project is read from a service account token, so `project_id` is usually not needed
* Waits for KKP's asynchronous reconciliation: active projects, healthy control planes, available replicas, finished deletions
* Retries machine deployment creation while KKP still answers 503 for a provisioning cluster
* In-place updates only where KKP really applies them; replacement for fields KKP silently ignores on PATCH
* Import support for every resource except tokens (KKP shows a token only once)

## Compatibility

### Cloud providers

A cluster sets the block of its datacenter's provider (`openstack = { … }`, `aws = { … }`, …), and its machine deployments set the same one. Credentials come from the block or from a KKP preset (`credential_preset`, with an empty block).

| Provider | `kkp_cluster` | `kkp_machine_deployment` | Credentials | Notes |
|---|:-:|:-:|---|---|
| OpenStack | ✅ | ✅ | user + password, or application credential | The provider's first target; network, subnet, router and security group are created by KKP unless set |
| AWS | ✅ | ✅ | access key (optionally assuming a role) | VPC, security group, route table and IAM resources default to KKP's; AMI defaults to the datacenter's image; spot instances; `amzn2` OS |
| Azure | ✅ | ✅ | service principal | Resource group, VNet, subnet, route table, security group and availability set default to KKP's; zones, accelerated networking |
| Google Cloud | ✅ | ✅ | service account key JSON (`file("sa.json")`; the provider base64-encodes it) | Network and subnetwork; zone defaults to the datacenter's; preemptible VMs |
| Hetzner Cloud | ✅ | ✅ | API token | Network defaults to the datacenter's |
| DigitalOcean | ✅ | ✅ | API token | Backups, monitoring, tags |
| VMware vSphere | ❌ | ❌ | | Planned: needs datastore/cluster/folder selection and VM templates |
| KubeVirt | ❌ | ❌ | | Planned: infra kubeconfig credential, instance types and storage classes |
| VMware Cloud Director, Nutanix, Anexia, Alibaba | ❌ | ❌ | | Not planned yet; open an issue if you need one |
| Edge, bring-your-own (kubeadm) | ❌ | ❌ | | Nodes join with a script instead of a machine deployment |

All providers share the same cluster settings (version, CNI, expose strategy, networking, audit logging, OPA, Kyverno, MLA, …). Changing the cloud provider or the datacenter replaces the cluster; KKP-created cloud resources are fixed at creation, so changing them also replaces it. Machine settings change in place: machine-controller rolls the nodes.

Operating systems: `ubuntu`, `flatcar`, `rhel`, `rockylinux`, and `amzn2` on AWS. Which ones a datacenter offers is up to the KKP installation.

### Tools and KKP versions

| | Supported | Tested in CI |
|---|---|---|
| OpenTofu | 1.6+ (plugin protocol 6) | latest |
| Terraform | 1.0+ (plugin protocol 6) | latest |
| KKP | 2.31 API (`/api/v1`, `/api/v2`); older 2.2x releases are likely to work | against the in-memory fake KKP in `internal/kkpmock` |

The provider has not been run against a real KKP installation yet; the fake mirrors the API behaviour the KKP dashboard work ran into (asynchronous reconciliation, 503s for machine deployments, credential stripping, the PATCH field whitelist). Reports from real clusters, especially on clouds other than OpenStack, are welcome.

## Usage

### 1. Get a token

In the KKP dashboard, open the project, go to **Service Accounts**, add one with the **Editor** role and create a token. KKP shows it once.

### 2. Configure the provider

```hcl
terraform {
  required_providers {
    kkp = {
      source = "samualfageme/kkp"
    }
  }
}

provider "kkp" {}
```

```shell
export KKP_HOST=https://kkp.example.com
export KKP_TOKEN=<service account token>
tofu init && tofu apply
```

A complete example (cluster, autoscaled workers, kubeconfig output) is in [examples/openstack-cluster](examples/openstack-cluster/main.tf).

## Development

```shell
make build
make test            # provider tests through OpenTofu against an in-memory fake KKP (internal/kkpmock)
make test-terraform  # the same tests through Terraform
make lint
make generate        # regenerate docs/ (make generate-tofu without access to HashiCorp's servers)
```

See [AGENTS.md](AGENTS.md) for conventions and the KKP API quirks the provider handles.
