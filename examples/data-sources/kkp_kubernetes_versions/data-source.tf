data "kkp_kubernetes_versions" "openstack" {
  cloud_provider = "openstack"
}

output "versions" {
  value = data.kkp_kubernetes_versions.openstack.versions
}
