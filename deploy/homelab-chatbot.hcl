job "homelab-chatbot" {
  node_pool   = "default"
  datacenters = ["dc1"]
  type        = "service"

  group "chatbot" {
    count = 1

    network {
      port "http" {
        to = 8080
      }
    }

    service {
      name     = "homelab-chatbot"
      port     = "http"
      provider = "consul"
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.homelab-chatbot.rule=Host(`chatbot.example.com`)",
        "traefik.http.routers.homelab-chatbot.entrypoints=websecure",
        "traefik.http.routers.homelab-chatbot.tls=true",
      ]

      check {
        type     = "http"
        path     = "/health"
        port     = "http"
        interval = "30s"
        timeout  = "5s"

        check_restart {
          limit = 3
          grace = "30s"
        }
      }
    }

    restart {
      attempts = 3
      interval = "2m"
      delay    = "15s"
      mode     = "fail"
    }

    vault {
      cluster     = "default"
      change_mode = "restart"
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

      template {
        data = <<EOF
{{ with secret "kv/data/nomad/default/homelab-chatbot" }}
ANTHROPIC_API_KEY={{ .Data.data.anthropic_api_key }}
SESSION_SECRET={{ .Data.data.session_secret }}
ADMIN_PASSWORD={{ .Data.data.admin_password }}
NOMAD_GATEWAY_KEY={{ .Data.data.nomad_gateway_key }}
ADGUARD_GATEWAY_KEY={{ .Data.data.adguard_gateway_key }}
CF_GATEWAY_KEY={{ .Data.data.cf_gateway_key }}
MINECRAFT_GATEWAY_KEY={{ .Data.data.minecraft_gateway_key }}
CURSEFORGE_GATEWAY_KEY={{ .Data.data.curseforge_gateway_key }}
VAULT_GATEWAY_KEY={{ .Data.data.vault_gateway_key }}
{{ end }}
EOF
        destination = "secrets/homelab-chatbot.env"
        env         = true
      }

      env {
        PORT                   = "8080"
        LOG_LEVEL              = "info"
        DATA_DIR               = "/data"
        MCP_SERVER_CMD         = "/app/homelab-mcp-server"
        CLAUDE_MODEL           = "claude-sonnet-4-6"
        NOMAD_GATEWAY_URL      = "http://nomad-gateway.example.com"
        ADGUARD_GATEWAY_URL    = "http://adguard-home-gateway.example.com"
        CF_GATEWAY_URL         = "http://cloudflare-gateway.example.com"
        MINECRAFT_GATEWAY_URL  = "http://minecraft-gateway.example.com"
        CURSEFORGE_GATEWAY_URL = "http://curseforge-gateway.example.com"
        VAULT_GATEWAY_URL      = "http://vault-gateway.example.com"
      }

      resources {
        cpu    = 2000
        memory = 512
      }

      kill_timeout = "35s"
    }
  }
}
