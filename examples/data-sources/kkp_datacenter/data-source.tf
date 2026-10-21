data "kkp_datacenter" "zurich" {
  name = "os-zurich"
}

output "ubuntu_image" {
  value = data.kkp_datacenter.zurich.images["ubuntu"]
}
