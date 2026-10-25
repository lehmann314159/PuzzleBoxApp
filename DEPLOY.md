# Deploying the Puzzle Box

These are the steps for the author's own setup: a home server (the "gx10") running Docker and Caddy, behind a small cloud
instance that terminates TLS. Port **9100**, subdomain **puzzlebox.verynormalserver.com**. Substitute your own host, domain
and registry. `<gx10-host>` below is the address of the home server as the cloud instance sees it.

1. **Image.** Push to GitHub; `.github/workflows/docker-publish.yml` tests, then builds `lehmann314159/puzzlebox`
   for amd64 and arm64 (needs the `DOCKERHUB_TOKEN` secret, as in the other repos).
2. **Model.** On the gx10: `ollama pull gemma4:12b`. The container reaches the host's Ollama through
   `host-gateway:11434`, the same way Terve does, so Ollama must listen beyond localhost (`OLLAMA_HOST=0.0.0.0`).
3. **Compose.** Copy `docker-compose.yml` and `.env.example` to `~/apps/puzzlebox/`, then:
   ```
   cp .env.example .env     # fill in UI_TOKEN, MCP_BEARER, BOX_SECRET with: openssl rand -hex 24
   docker compose pull && docker compose up -d
   ```
   Compose refuses to start without `UI_TOKEN` and `MCP_BEARER`.
4. **Caddy on the gx10**, in `/etc/caddy/Caddyfile`, then `sudo systemctl reload caddy`:
   ```
   @puzzlebox host puzzlebox.verynormalserver.com
   handle @puzzlebox {
       reverse_proxy localhost:9100
   }
   ```
5. **Caddy on the Lightsail instance**, then `sudo systemctl reload caddy` (without this TLS fails):
   ```
   puzzlebox.verynormalserver.com {
       reverse_proxy http://<gx10-host>:18080 {
           header_up Host {host}
           transport http {
               dial_timeout 10s
               response_header_timeout 300s
           }
       }
   }
   ```
   `response_header_timeout` matters here: an Agent turn can take a while on a 24B model. Also add the subdomain to the
   Lightsail load balancer's TLS certificate (the last item of the author's service checklist).
6. **frontPage.** Add the app at `https://verynormalserver.com/admin/apps` (Apps are database rows, not config):
   - Name: `Puzzle Box`
   - URL: `https://puzzlebox.verynormalserver.com/?t=<UI_TOKEN>` (the token in the link is what lets visitors in without admin;
     leave it off if you would rather hand the token out separately)
   - Description: `A brass box, an MCP server, and a chat that can open it. Watch the protocol as it happens.`
   - Icon: `🧰`
   - Source URL: the GitHub repo.

Connect an MCP client:
`claude mcp add --transport http puzzlebox https://puzzlebox.verynormalserver.com/mcp --header "Authorization: Bearer <MCP_BEARER>"`
