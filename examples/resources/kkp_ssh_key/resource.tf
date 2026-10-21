resource "kkp_ssh_key" "ops" {
  name       = "ops"
  public_key = file("~/.ssh/id_ed25519.pub")
}
