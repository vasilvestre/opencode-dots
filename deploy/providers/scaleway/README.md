# Scaleway provisioning

OpenTofu module for the provisioning layer of
[ADR 0028](../../../ADR/0028-deployment-is-code-in-two-layers.md). It creates
one instance, one flexible IPv4, one SSH key and one security group. The
instance gets a `dots` user with sudo and your key. The bootstrap scripts do
the rest.

Inbound traffic is dropped except SSH from `ssh_allowed_cidrs`. Outbound is open.

## Requirements

- OpenTofu 1.7 or later (native state encryption). Checked with 1.13.
- A Scaleway project and an API key pair.

## Run

```bash
cd deploy/providers/scaleway
cp terraform.tfvars.example terraform.tfvars   # edit it, it is ignored by git
export SCW_ACCESS_KEY=...
export SCW_SECRET_KEY=...
export TF_ENCRYPTION='
key_provider "pbkdf2" "main" {
  passphrase = "a long passphrase, 16 characters at least"
}
method "aes_gcm" "main" {
  keys = key_provider.pbkdf2.main
}
state {
  method   = method.aes_gcm.main
  enforced = true
}
'
tofu init
tofu plan
tofu apply
```

Then `ssh dots@$(tofu output -raw public_ip)`.

State is local (`terraform.tfstate`) and encrypted with the passphrase. Keep
the passphrase outside the repository and back it up: without it the state is
lost. Never put it in a file here.

## Variables

| Name | Meaning | Default |
| --- | --- | --- |
| `project_id` | Scaleway project that owns the resources | required |
| `ssh_public_key` | Admin public key, registered and given to `dots` | required |
| `ssh_allowed_cidrs` | CIDR blocks allowed on port 22 | required |
| `zone` | Availability zone | `pl-waw-2` |
| `type` | Instance type, 2 GB at least | `DEV1-S` |
| `image` | Image label or UUID | `debian_trixie` |
| `name` | Name of the instance and companions | `dots` |

## Remove the SSH rule

When the tailnet is the only way in, set `ssh_allowed_cidrs = []` and run
`tofu apply`. The group then has no inbound rule at all. To open it again, put
a CIDR back.

## Not verified

- No `plan` or `apply` was run: nothing here has touched the Scaleway API.
- `tofu validate` only checks syntax and schema.
- That `debian_trixie` is the right image label for `DEV1-S` in `pl-waw-2`.
- That `DEV1-S` exists in `pl-waw-2` and accepts a `routed_ipv4` flexible IP and a security group.
- That cloud-init runs on the image and that the IAM SSH key is also placed
  for root as expected.
- The minimum version 1.7 for state encryption comes from the release notes as
  I know them. The documentation page confirms `TF_ENCRYPTION` and the
  passphrase provider but does not state the version.
- Behaviour of `ignore_changes` on `user_data` and `image` over time.
