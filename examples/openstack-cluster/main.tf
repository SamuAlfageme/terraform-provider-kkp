# A complete OpenStack cluster: SSH key, cluster, one autoscaled machine deployment and a kubeconfig.
#
#   export KKP_HOST=https://kkp.example.com KKP_TOKEN=<service account token>
#   tofu init && tofu apply -var preset=<openstack preset> -var ssh_public_key="$(cat ~/.ssh/id_ed25519.pub)"

terraform {
  required_providers {
    kkp = {
      source = "samualfageme/kkp"
    }
  }
}

provider "kkp" {}

variable "preset" {
  description = "KKP preset holding the OpenStack credentials."
  type        = string
}

variable "ssh_public_key" {
  description = "OpenSSH public key to put on the nodes, e.g. the contents of ~/.ssh/id_ed25519.pub."
  type        = string
}

variable "datacenter" {
  type    = string
  default = null
}

data "kkp_datacenters" "openstack" {
  cloud_provider = "openstack"
}

data "kkp_kubernetes_versions" "openstack" {}

locals {
  datacenter = coalesce(var.datacenter, data.kkp_datacenters.openstack.names[0])
}

data "kkp_datacenter" "this" {
  name = local.datacenter
}

resource "kkp_ssh_key" "ops" {
  name       = "ops"
  public_key = var.ssh_public_key
}

resource "kkp_cluster" "this" {
  name              = "demo"
  datacenter        = local.datacenter
  version           = data.kkp_kubernetes_versions.openstack.default_version
  credential_preset = var.preset
  openstack         = {}
  ssh_key_ids       = [kkp_ssh_key.ops.id]

  delete_volumes_on_destroy        = true
  delete_load_balancers_on_destroy = true
}

resource "kkp_machine_deployment" "workers" {
  cluster_id       = kkp_cluster.this.id
  name             = "workers"
  replicas         = 2
  min_replicas     = 1
  max_replicas     = 5
  operating_system = "ubuntu"

  openstack = {
    flavor = "m1.large"
    image  = data.kkp_datacenter.this.images["ubuntu"]
  }
}

data "kkp_cluster_kubeconfig" "this" {
  cluster_id = kkp_cluster.this.id
}

output "kubeconfig" {
  value     = data.kkp_cluster_kubeconfig.this.kubeconfig
  sensitive = true
}
