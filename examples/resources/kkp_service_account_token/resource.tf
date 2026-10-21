resource "kkp_service_account_token" "ci" {
  service_account_id = kkp_service_account.ci.id
  name               = "github-actions"
}

output "ci_token" {
  value     = kkp_service_account_token.ci.token
  sensitive = true
}
