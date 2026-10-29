resource "kkp_cluster_service_account" "ci" {
  cluster_id    = kkp_cluster.staging.id
  name          = "ci"
  cluster_roles = ["cluster-admin"]
}

provider "kubernetes" {
  config_path = local_sensitive_file.kubeconfig.filename
}

resource "local_sensitive_file" "kubeconfig" {
  content  = kkp_cluster_service_account.ci.kubeconfig
  filename = "${path.module}/ci.kubeconfig"
}
