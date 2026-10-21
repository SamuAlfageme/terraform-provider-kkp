terraform {
  required_providers {
    kkp = {
      source = "samualfageme/kkp"
    }
  }
}

# Credentials usually come from the environment:
#   export KKP_HOST=https://kkp.example.com
#   export KKP_TOKEN=<project service account token>
# A service account token names its project, so project_id can be left out.
provider "kkp" {}
