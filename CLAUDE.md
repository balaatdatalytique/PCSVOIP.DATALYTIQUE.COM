use /home/production/SOURCE/ folders for project references. It contain critical design principles that are guidelines, but always do your own research and due diligence.

## Production Server

- **Host**: srv1329845 (76.13.124.114)
- **SSH user**: balachandran
- **SSH access**: SSH keys for `balachandran@macbook` and `bala.chandran@datalytique.com` are authorized
- **OS**: Debian Linux (6.12.57+deb13-amd64)
- **Deploy path**: `/home/production/DEPLOYED/pcsvoip.datalytique.com`
- **Site URL**: https://pcsvoip.datalytique.com

## Remote Deployment (from laptop)

To update the site remotely without logging into the server:

1. Clone the repo: `git clone git@github.com:DATALYTIQUE-INC/pcsvoip.datalytique.com.git`
2. Make changes locally and push to `main`
3. SSH into the server and pull + deploy:
   ```bash
   ssh balachandran@76.13.124.114 "cd /home/production/DEPLOYED/pcsvoip.datalytique.com && git pull origin main && bash deploy.sh"
   ```

Or run the steps separately:
```bash
# Pull latest code
ssh balachandran@76.13.124.114 "cd /home/production/DEPLOYED/pcsvoip.datalytique.com && git pull origin main"

# Deploy (rebuild containers + healthcheck)
ssh balachandran@76.13.124.114 "cd /home/production/DEPLOYED/pcsvoip.datalytique.com && bash deploy.sh"

# Deploy only web or voice-proxy
ssh balachandran@76.13.124.114 "cd /home/production/DEPLOYED/pcsvoip.datalytique.com && bash deploy.sh web"
ssh balachandran@76.13.124.114 "cd /home/production/DEPLOYED/pcsvoip.datalytique.com && bash deploy.sh voice"

# Check health
ssh balachandran@76.13.124.114 "cd /home/production/DEPLOYED/pcsvoip.datalytique.com && bash healthcheck.sh"

# View logs
ssh balachandran@76.13.124.114 "docker logs pcsvoip-web --tail 30"
ssh balachandran@76.13.124.114 "docker logs pcsvoip-voice --tail 30"
```

## Architecture

- **pcsvoip-web** (container: `pcsvoip-web`, port 9080) — Go web server + admin panel, serves static HTML/CSS/JS, bbolt DB for admin data
- **voice-proxy** (container: `pcsvoip-voice`, port 9081) — Go service for text chat (REST), voice chat (WebSocket), phone callbacks (FreeSWITCH + Grok Realtime API)
- **nginx-proxy** (container: `nginx-proxy`) — SSL termination + reverse proxy, runs from `/home/production/DEPLOYED/smartcrm.pcsvoip.com`. Must be started FIRST.
- **Network**: Both containers join the `smartcrmpcsvoipcom_default` Docker network (external, from smartcrm stack)

### Routing (nginx → containers)
| Path | Routes to |
|------|-----------|
| `/` (all pages) | pcsvoip-web:9080 |
| `/admin/*` | pcsvoip-web:9080 |
| `/api/chat`, `/api/chat/end` | pcsvoip-voice:9081 |
| `/api/callback` | pcsvoip-voice:9081 |
| `/ws/voice` | pcsvoip-voice:9081 |

## Environment Variables (.env)

The `.env` file at the project root contains secrets (API keys, SMTP credentials, etc.) and is NOT committed to git. Key variable groups:
- **GROK_API_KEY** — xAI API key for Grok (text chat + voice)
- **SMTP_*** — Email sending (host, port, user, pass, from, admin email)
- **FREESWITCH_*** — FreeSWITCH ESL for phone callbacks
- **ADMIN_MASTER_KEY** — Admin panel encryption key
- **INTERNAL_API_TOKEN** — Inter-service auth token (web ↔ voice-proxy)
- **CRM_*** — Pegasi CRM integration (appointments)

## Git Remotes

- `origin` → `git@github.com:balaatdatalytique/PCSVOIP.DATALYTIQUE.COM.git`
- `datalytique` → `git@github.com:DATALYTIQUE-INC/pcsvoip.datalytique.com.git`

Push to both remotes when committing: `git push origin main && git push datalytique main`

## Deploy Safety Rules

- Always use `deploy.sh` + `healthcheck.sh` — never raw `docker compose build` on production
- Static files (HTML/CSS/JS) are baked into the Docker image — container must be rebuilt for changes to take effect
- The smartcrm/nginx-proxy stack must be running before deploying this stack
