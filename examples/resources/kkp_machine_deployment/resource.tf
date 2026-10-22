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

resource "kkp_machine_deployment" "aws_spot" {
  cluster_id       = kkp_cluster.aws.id
  name             = "spot"
  replicas         = 2
  operating_system = "amzn2"

  aws = {
    instance_type    = "t3.large"
    disk_size        = 50
    volume_type      = "gp3"
    is_spot_instance = true
  }
}

resource "kkp_machine_deployment" "hetzner" {
  cluster_id       = kkp_cluster.hetzner.id
  name             = "workers"
  replicas         = 3
  operating_system = "ubuntu"
  hetzner          = { type = "cx32" }
}
