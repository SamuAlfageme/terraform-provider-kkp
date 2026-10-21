resource "kkp_machine_deployment" "workers" {
  cluster_id       = kkp_cluster.staging.id
  name             = "workers"
  replicas         = 3
  min_replicas     = 1
  max_replicas     = 6
  operating_system = "ubuntu"

  node_labels = { pool = "general" }
  taints      = [{ key = "dedicated", value = "batch", effect = "NoSchedule" }]

  openstack = {
    flavor    = "m1.large"
    image     = "Ubuntu 24.04"
    disk_size = 50
  }
}
