output "public_ip" {
  description = "Public IPv4 address, for the first SSH login."
  value       = scaleway_instance_ip.this.address
}

output "server_id" {
  description = "Instance identifier."
  value       = scaleway_instance_server.this.id
}
