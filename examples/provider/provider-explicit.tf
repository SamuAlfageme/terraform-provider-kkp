provider "kkp" {
  alias      = "explicit"
  host       = "https://kkp.example.com"
  token_file = pathexpand("~/.config/kkp/token")
  project_id = "abcd1234ef"

  # For installations with a private CA:
  # ca_certificate = file("ca.pem")
}
