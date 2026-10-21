data "kkp_kubernetes_versions" "openstack" {}

resource "kkp_cluster" "staging" {
  name        = "staging"
  datacenter  = "os-zurich"
  version     = data.kkp_kubernetes_versions.openstack.default_version
  labels      = { env = "staging" }
  ssh_key_ids = [kkp_ssh_key.ops.id]

  openstack = {
    # Or set credential_preset on the cluster instead.
    application_credential_id     = var.os_application_credential_id
    application_credential_secret = var.os_application_credential_secret
  }

  # Clean up cloud resources the cluster created.
  delete_volumes_on_destroy        = true
  delete_load_balancers_on_destroy = true

  timeouts = {
    create = "20m"
  }
}
