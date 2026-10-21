data "kkp_cluster_kubeconfig" "staging" {
  cluster_id = kkp_cluster.staging.id
}

provider "kubernetes" {
  config_path = local_sensitive_file.kubeconfig.filename
}

resource "local_sensitive_file" "kubeconfig" {
  content  = data.kkp_cluster_kubeconfig.staging.kubeconfig
  filename = "${path.module}/kubeconfig"
}
