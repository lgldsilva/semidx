# Placeholder-only Vault Agent config. Do not put role_id/secret_id here.
vault {
  address = "https://vault.example.invalid"
}

pid_file = "/tmp/vault-agent.pid"

auto_auth {
  method "approle" {
    mount_path = "auth/approle"
    config = {
      role_id_file_path = "/run/secrets/vault_role_id"
      secret_id_file_path = "/run/secrets/vault_secret_id"
    }
  }
  sink "file" {
    config = {path = "/vault/secrets/vault-token", mode = 0600}
  }
}

# The only required key for startup is db_password. Optional files are picked
# up by the image entrypoint when present; add local template blocks for them.
template {
  contents = <<EOT
{{- with secret "secret/data/semidx/staging" -}}{{ .Data.data.db_password }}{{- end -}}
EOT
  destination = "/vault/secrets/db_password"
  perms = "0600"
  error_on_missing_key = true
}
