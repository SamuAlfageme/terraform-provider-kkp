# Creating projects needs a user token (KKP refuses service account tokens here).
resource "kkp_project" "platform" {
  name   = "platform"
  labels = { team = "infra" }
}
