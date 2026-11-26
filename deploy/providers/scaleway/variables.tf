variable "project_id" {
  type        = string
  description = "Scaleway project that owns the resources."
}

variable "ssh_public_key" {
  type        = string
  description = "Public key of the admin, registered at Scaleway and given to the dots user."
}

variable "ssh_allowed_cidrs" {
  type        = list(string)
  description = "CIDR blocks allowed to reach SSH. No default: set it on purpose. Use [] to close SSH."
}

variable "zone" {
  type        = string
  description = "Availability zone."
  default     = "pl-waw-2"
}

variable "type" {
  type        = string
  description = "Instance type. The dot needs 2 GB: on a 1 GB machine (STARDUST1-S) an agent run froze the first server. DEV1-S has 2 GB; check that the type exists in the zone before applying."
  default     = "DEV1-S"
}

variable "image" {
  type        = string
  description = "Image label or UUID."
  default     = "debian_trixie"
}

variable "name" {
  type        = string
  description = "Name given to the instance and its companions."
  default     = "dots"
}
