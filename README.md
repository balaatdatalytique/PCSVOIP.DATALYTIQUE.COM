# PCSVOIP.DATALYTIQUE.COM

Production website and AI-powered CMS for PCSVoIP, built with Go.

## Overview

This repository contains:

- **Static website** — Full HTML/CSS/JS marketing site for PCSVoIP business phone services
- **Go CMS server** — Admin panel with file browser, inline editor, live preview, and AI-assisted content editing
- **AI editing engine** — Supports OpenAI, Anthropic (Claude), and Grok for surgical content edits with diff-based review
- **Chatbot** — AI-powered chatbot on all pages with auto-popup and pulse animation

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Backend | Go 1.25 (net/http, html/template) |
| Frontend (site) | HTML5, CSS3, Bootstrap 5.3, jQuery |
| Frontend (admin) | Vanilla HTML/CSS/JS, VS Code light theme |
| AI Providers | OpenAI (GPT-4), Anthropic (Claude), Grok |
| Auth | bcrypt passwords, session-based cookies |
| Containerization | Docker, Docker Compose |
| Deployment | Docker container, Nginx reverse proxy, Let's Encrypt SSL |

## Project Structure

```
.
├── cmd/server/              # Main entry point (main.go)
├── internal/
│   ├── ai/                  # AI provider interface (OpenAI, Anthropic, Grok)
│   ├── auth/                # Session-based authentication & bcrypt
│   ├── cms/                 # CMS service layer (load, save, AI edit)
│   ├── config/              # CLI flags and environment config
│   ├── middleware/          # Auth, logging, recovery middleware
│   ├── routes/              # HTTP route handlers (admin + static)
│   ├── server/              # HTTP server setup
│   └── storage/             # File I/O with backup and path traversal protection
├── web/templates/           # Admin HTML templates (login, dashboard, editor, ai)
├── assets/                  # Static assets
│   ├── css/                 # Site stylesheets (style2.css, chatbot.css, gallery.css, etc.)
│   ├── js/                  # Site JavaScript (main.js, chatbot.js, gallery.js, etc.)
│   ├── img/                 # Site images
│   ├── fonts/               # Font Awesome fonts
│   ├── includes/            # Reusable HTML includes (chatbot.html)
│   └── videos/              # Video content (not in git, mount as volume)
├── tests/                   # Playwright end-to-end tests
├── *.html                   # Static site pages (index, about, solutions, compare, etc.)
├── Dockerfile               # Multi-stage Docker build
├── docker-compose.yml       # Docker Compose orchestration
├── .dockerignore            # Docker build exclusions
├── go.mod / go.sum          # Go module dependencies
├── Makefile                 # Build commands (native)
└── CLAUDE.md                # AI assistant instructions
```

## Quick Start (Docker)

### Prerequisites

- Docker Engine 20.10+
- Docker Compose v2+
- `.env` file with required environment variables

### 1. Configure Environment

```bash
cp .env.example .env
# Edit .env with your actual API keys and credentials
```

### 2. Build and Run

```bash
# Build and start the container
docker compose up -d --build

# Verify it's running
docker compose ps
docker compose logs -f pcsvoip-web
```

### 3. Verify

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/
# Should return: 200

curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/admin/login
# Should return: 200
```

The site is now accessible at `http://localhost:9080`.

## Production Deployment (Docker)

### Full Deployment Procedure

```bash
# 1. SSH into the production server
ssh production@your-server

# 2. Navigate to the project directory
cd /home/production/DEPLOYED/pcsvoip.datalytique.com

# 3. Pull latest changes (if using git)
git pull origin main

# 4. Ensure .env file exists with production credentials
# (Never commit .env — it contains API keys and passwords)
cat .env  # Verify it exists and has correct values

# 5. Build the Docker image
docker compose build

# 6. Stop the old container and start the new one
docker compose down
docker compose up -d

# 7. Verify the container is healthy
docker compose ps
docker compose logs --tail=50 pcsvoip-web

# 8. Test the site
curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/
curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/admin/login

# 9. Check container health
docker inspect --format='{{.State.Health.Status}}' pcsvoip-web
# Should return: healthy
```

### Zero-Downtime Redeployment

```bash
# Rebuild and restart without downtime
docker compose up -d --build --force-recreate

# Monitor logs during startup
docker compose logs -f pcsvoip-web
```

### Rollback

```bash
# If something goes wrong, revert to previous image
docker compose down
git checkout HEAD~1
docker compose up -d --build
```

### Nginx Reverse Proxy Configuration

Nginx sits in front of the Docker container handling SSL termination:

```
Internet (HTTPS/443) → Nginx → Docker container (HTTP/9080)
```

Example Nginx config (`/etc/nginx/sites-available/pcsvoip.datalytique.com`):

```nginx
server {
    listen 443 ssl http2;
    server_name pcsvoip.datalytique.com;

    ssl_certificate /etc/letsencrypt/live/pcsvoip.datalytique.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/pcsvoip.datalytique.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:9080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

server {
    listen 80;
    server_name pcsvoip.datalytique.com;
    return 301 https://$host$request_uri;
}
```

### SSL Certificate Renewal

```bash
# Certificates auto-renew via certbot cron/timer
sudo certbot renew --dry-run

# Manual renewal if needed
sudo certbot certonly --webroot -w /var/www/html -d pcsvoip.datalytique.com
sudo nginx -s reload
```

## Native Build & Run (Without Docker)

### Prerequisites

- Go 1.25+ installed at `/usr/local/go/bin/go`
- `.env` file with required environment variables

### Commands

```bash
make build          # Compile binary to ./bin/webserver
make run            # Build and run on port 8080
make clean          # Remove compiled binaries
```

### Manual Run

```bash
./bin/webserver -port 9080 -contentDir /path/to/site
```

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8080` | HTTP server port |
| `-contentDir` | project root | Root directory for static content and file editing |

### Native Production Deployment

```bash
# Build and restart
make build
kill $(pgrep -f "webserver.*9080") 2>/dev/null
nohup ./bin/webserver -port 9080 > /tmp/pcsvoip-webserver.log 2>&1 &

# Verify
curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/admin/login
```

## Environment Variables (.env)

Create a `.env` file in the project root (never commit this file):

```bash
# AI Provider (required for AI editing)
AI_PROVIDER=grok              # Options: openai, anthropic, grok
GROK_API_KEY=xai-...          # API key for chosen provider
# OPENAI_API_KEY=sk-...
# ANTHROPIC_API_KEY=sk-ant-...

# CMS Admin (optional, has defaults)
CMS_ADMIN_USER=admin          # Admin login username
CMS_ADMIN_PASS=<bcrypt hash>  # Bcrypt-hashed admin password
CMS_SESSION_KEY=<secret>      # Session encryption key
CMS_AI_MODEL=<model>          # Override default model per provider
```

## Admin Panel

Access at `https://pcsvoip.datalytique.com/admin/login`

### Routes

| Route | Method | Description |
|-------|--------|-------------|
| `/admin/login` | GET/POST | Login page |
| `/admin/dashboard` | GET | File browser with directory navigation |
| `/admin/edit?file=` | GET | Code editor with syntax-aware textarea |
| `/admin/save` | POST | Save edited content (auto-backup created) |
| `/admin/preview` | POST | Live preview in iframe with base tag injection |
| `/admin/ai?file=` | GET | AI content editor interface |
| `/admin/ai` | POST | Submit AI edit instruction (returns JSON) |
| `/admin/ai/approve` | POST | Approve and save AI-generated content |
| `/admin/logout` | GET | Destroy session and redirect to login |

### AI Editing Workflow

1. Navigate to a file in the dashboard and click **AI**
2. Enter an instruction (e.g., "Improve the SEO meta tags", "Rewrite the hero section")
3. The AI generates surgical edits using JSON find/replace operations
4. Review the side-by-side diff (original vs. AI-generated)
5. **Approve & Save** or **Discard** the changes
6. A backup is automatically created before any save

## Website Pages

| Page | File | Description |
|------|------|-------------|
| Home | `index.html` | Main landing page |
| About | `about.html` | Company information |
| FAQ | `faq.html` | Frequently asked questions |
| Contact | `contact.html` | Contact form |
| VoIP Services | `voip.html` | VoIP services overview |
| Solutions | `solutions.html` | Industry solutions showcase |
| Compare Providers | `compare.html` | VoIP provider comparison tool |
| Features | `features.html` | Feature breakdown |
| Clients | `clients.html` | Client showcase |
| Testimonials | `testimonials.html` | Customer testimonials |
| Videos | `videos.html` | Video gallery |
| Privacy | `privacy.html` | Privacy policy |
| Terms | `terms.html` | Terms of service |
| Phones | `phones.html` | Hardware phones catalog |
| PBX/SIP Trunk | `pbx-sip-trunk.html` | PBX and SIP trunking |
| Contact Center | `contact-center.html` | Contact center solution |
| Cloud SMS | `cloud-sms.html` | Enterprise Cloud SMS with tier comparison |
| E-Fax | `e-fax.html` | Electronic fax service |
| UC Client | `uc-client.html` | Unified communications client |
| Video Conferencing | `video-conferencing.html` | Video conferencing & contact sharing |
| Mobile App | `mobile-application.html` | Mobile application features |
| Workforce Mgmt | `workforce-management.html` | Workforce management |
| API Integration | `api-integration.html` | API integration details |
| API Videos | `api-videos.html` | API demo videos |
| SMS | `sms.html` | Standard SMS service |
| Quote | `quote.html` | Request a quote |
| Elite Plan | `elite.html` | Elite pricing plan |
| Premier Plan | `premier.html` | Premier pricing plan |

## Docker Commands Reference

```bash
# Build the image
docker compose build

# Start in background
docker compose up -d

# View logs (follow)
docker compose logs -f pcsvoip-web

# Stop the container
docker compose down

# Rebuild and restart
docker compose up -d --build --force-recreate

# Shell into running container
docker compose exec pcsvoip-web sh

# Check container health
docker inspect --format='{{.State.Health.Status}}' pcsvoip-web

# View resource usage
docker stats pcsvoip-web

# Clean up unused images
docker image prune -f
```

## Security Features

- All `/admin/*` routes (except login) require authenticated session
- Passwords stored as **bcrypt hashes** (never plaintext)
- Session-based auth with `HttpOnly`, `SameSite=Strict` cookies
- **Path traversal protection** on all file operations
- **Automatic backup** before every file write (stored in `.cms-backups/`)
- AI content requires **explicit admin approval** before saving
- `.env` excluded from version control and Docker image
- Docker container runs as non-root user (`pcsvoip`)

## Files NOT in Git

These are excluded via `.gitignore` and must be managed separately:

| Path | Reason |
|------|--------|
| `.env` | Contains API keys and secrets |
| `bin/` | Compiled Go binaries (rebuild with `make build`) |
| `assets/apivideos/` | ~230MB of demo videos (deploy via volume mount) |
| `assets/videos/` | Video content (deploy via volume mount) |
| `.cms-backups/` | Runtime backup files created by the CMS |
| `node_modules/` | Test dependencies (install with `npm install`) |
| `.well-known/` | Certbot ACME challenge files |

## Maintenance

### Adding a New Page

1. Create the `.html` file in the project root
2. The static file server will automatically serve it
3. Add navigation links in the relevant menus (both mobile and desktop nav) across all pages
4. Rebuild the Docker image: `docker compose up -d --build`

### Changing AI Provider

Update `.env`:
```bash
AI_PROVIDER=anthropic          # or openai, grok
ANTHROPIC_API_KEY=sk-ant-...   # Key for the chosen provider
```
Restart the container: `docker compose restart pcsvoip-web`

### Updating Admin Templates

Templates are in `web/templates/`. After editing:
```bash
docker compose up -d --build --force-recreate
```

### Monitoring

```bash
# Container status
docker compose ps

# Live logs
docker compose logs -f pcsvoip-web

# Health check
curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/

# Resource usage
docker stats pcsvoip-web --no-stream
```

### Troubleshooting

```bash
# Container won't start
docker compose logs pcsvoip-web

# Check if port is in use
ss -tlnp | grep 9080

# Rebuild from scratch
docker compose down
docker compose build --no-cache
docker compose up -d

# Check .env is being loaded
docker compose exec pcsvoip-web env | grep CMS
```
