terraform {
  # Native state encryption needs OpenTofu 1.7 or later.
  required_version = ">= 1.7.0"

  required_providers {
    scaleway = {
      source  = "scaleway/scaleway"
      version = "~> 2.0"
    }
  }
}

# Credentials come from SCW_ACCESS_KEY and SCW_SECRET_KEY.
provider "scaleway" {
  project_id = var.project_id
  zone       = var.zone
}
