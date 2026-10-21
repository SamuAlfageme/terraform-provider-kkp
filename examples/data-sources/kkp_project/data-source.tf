# The provider's default project (for a service account token, the token's project).
data "kkp_project" "current" {}

data "kkp_project" "by_name" {
  name = "platform"
}
