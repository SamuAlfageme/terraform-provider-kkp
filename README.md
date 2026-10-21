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
| Clusters (OpenStack) | [`kkp_cluster`](docs/resources/cluster.md) | [`kkp_cluster`](docs/data-sources/cluster.md), [`kkp_cluster_kubeconfig`](docs/data-sources/cluster_kubeconfig.md) |
| Machine deployments (OpenStack) | [`kkp_machine_deployment`](docs/resources/machine_deployment.md) | – |
| SSH keys | [`kkp_ssh_key`](docs/resources/ssh_key.md) | – |
| Service accounts and tokens | [`kkp_service_account`](docs/resources/service_account.md), [`kkp_service_account_token`](docs/resources/service_account_token.md) | – |
| Project members | [`kkp_project_member`](docs/resources/project_member.md) | – |
| Datacenters, versions | – | [`kkp_datacenter`](docs/data-sources/datacenter.md), [`kkp_datacenters`](docs/data-sources/datacenters.md), [`kkp_kubernetes_versions`](docs/data-sources/kubernetes_versions.md) |

* Bearer token authentication; the project is read from a service account token, so `project_id` is usually not needed
* Waits for KKP's asynchronous reconciliation: active projects, healthy control planes, available replicas, finished deletions
* Retries machine deployment creation while KKP still answers 503 for a provisioning cluster
* In-place updates only where KKP really applies them; replacement for fields KKP silently ignores on PATCH
* Import support for every resource except tokens (KKP shows a token only once)

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
