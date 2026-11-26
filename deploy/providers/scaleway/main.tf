resource "scaleway_iam_ssh_key" "admin" {
  name       = "${var.name}-admin"
  public_key = var.ssh_public_key
  project_id = var.project_id
}

resource "scaleway_instance_ip" "this" {
  type = "routed_ipv4"
}

# Inbound is dropped except SSH. Remove the rule with ssh_allowed_cidrs = []
# once the NetBird network is the only way in.
resource "scaleway_instance_security_group" "this" {
  name                    = "${var.name}-ssh-only"
  inbound_default_policy  = "drop"
  outbound_default_policy = "accept"
  stateful                = true

  dynamic "inbound_rule" {
    for_each = var.ssh_allowed_cidrs
    content {
      action   = "accept"
      protocol = "TCP"
      port     = 22
      ip_range = inbound_rule.value
    }
  }
}

resource "scaleway_instance_server" "this" {
  name              = var.name
  type              = var.type
  image             = var.image
  ip_id             = scaleway_instance_ip.this.id
  security_group_id = scaleway_instance_security_group.this.id

  user_data = {
    "cloud-init" = templatefile("${path.module}/cloud-init.yaml.tftpl", {
      ssh_public_key = var.ssh_public_key
    })
  }

  # The key resource must exist before the first boot.
  depends_on = [scaleway_iam_ssh_key.admin]

  # Later edits to cloud-init or the image must not rebuild a live machine.
  lifecycle {
    ignore_changes = [user_data, image]
  }
}
