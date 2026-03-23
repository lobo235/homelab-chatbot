job "homelab-chatbot" {
  datacenters = ["dc1"]
  type        = "service"
  node_pool   = "default"

  group "chatbot" {
    count = 1

    network {
      port "http" {
        to = 8080
      }
    }

    task "chatbot" {
      driver = "docker"

      config {
        image = "ghcr.io/lobo235/homelab-chatbot:latest"
        ports = ["http"]

        volumes = [
          "/path/to/chatbot/data:/data",
          "/path/to/mcp-server/data:/mcp-data",
        ]
      }

      env {
        PORT            = "${NOMAD_PORT_http}"
        DATA_DIR        = "/data"
        MCP_SERVER_CMD  = "/app/homelab-mcp-server"
        CLAUDE_MODEL    = "claude-sonnet-4-6"
        LOG_LEVEL       = "info"
      }

      # Secrets injected via Vault Workload Identity template.
      template {
        data        = <<-EOT
          {{ with secret "kv/data/nomad/default/homelab-chatbot" }}
          ANTHROPIC_API_KEY={{ .Data.data.anthropic_api_key }}
          SESSION_SECRET={{ .Data.data.session_secret }}
          ADMIN_PASSWORD={{ .Data.data.admin_password }}
          {{ end }}
          # Gateway env vars passed to MCP server subprocess.
          {{ with secret "kv/data/nomad/default/homelab-mcp-server" }}
          NOMAD_GATEWAY_URL={{ .Data.data.nomad_gateway_url }}
          NOMAD_GATEWAY_KEY={{ .Data.data.nomad_gateway_key }}
          ADGUARD_GATEWAY_URL={{ .Data.data.adguard_gateway_url }}
          ADGUARD_GATEWAY_KEY={{ .Data.data.adguard_gateway_key }}
          CF_GATEWAY_URL={{ .Data.data.cf_gateway_url }}
          CF_GATEWAY_KEY={{ .Data.data.cf_gateway_key }}
          MINECRAFT_GATEWAY_URL={{ .Data.data.minecraft_gateway_url }}
          MINECRAFT_GATEWAY_KEY={{ .Data.data.minecraft_gateway_key }}
          CURSEFORGE_GATEWAY_URL={{ .Data.data.curseforge_gateway_url }}
          CURSEFORGE_GATEWAY_KEY={{ .Data.data.curseforge_gateway_key }}
          VAULT_GATEWAY_URL={{ .Data.data.vault_gateway_url }}
          VAULT_GATEWAY_KEY={{ .Data.data.vault_gateway_key }}
          {{ end }}
        EOT
        destination = "secrets/env"
        env         = true
      }

      resources {
        cpu    = 2000
        memory = 512
      }

      service {
        name = "homelab-chatbot"
        port = "http"

        tags = [
          "traefik.enable=true",
          "traefik.http.routers.homelab-chatbot.rule=Host(`chatbot.example.com`)",
          "traefik.http.routers.homelab-chatbot.tls=true",
        ]

        check {
          type     = "http"
          path     = "/health"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }
  }
}
