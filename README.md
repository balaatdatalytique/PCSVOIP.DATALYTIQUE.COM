# PCSVOIP.DATALYTIQUE.COM

Production website and AI-powered CMS for PCSVoIP, built with Go.

## Overview

This repository contains:

- **Static website** — Full HTML/CSS/JS marketing site for PCSVoIP business phone services
- **Go CMS server** — Admin panel with file browser, inline editor, live preview, and AI-assisted content editing
- **AI editing engine** — Supports OpenAI, Anthropic (Claude), and Grok for surgical content edits with diff-based review

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Backend | Go (net/http, html/template) |
| Frontend (site) | HTML5, CSS3, Bootstrap, jQuery |
| Frontend (admin) | Vanilla HTML/CSS/JS, VS Code light theme |
| AI Providers | OpenAI (GPT-4), Anthropic (Claude), Grok |
| Auth | bcrypt passwords, session-based cookies |
| Deployment | Bare metal Linux, Nginx reverse proxy, Let's Encrypt SSL |

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
├── assets/                  # Static assets (CSS, JS, images, fonts)
│   ├── css/                 # Site stylesheets
│   ├── js/                  # Site JavaScript
│   ├── img/                 # Site images
│   ├── fonts/               # Font Awesome fonts
│   └── login/               # Login page assets
├── tests/                   # Playwright end-to-end tests
├── *.html                   # Static site pages (index, about, contact, etc.)
├── go.mod / go.sum          # Go module dependencies
├── Makefile                 # Build commands
└── CLAUDE.md                # AI assistant instructions
```

## Build & Run

### Prerequisites

- Go 1.25+ installed at `/usr/local/go/bin/go`
- `.env` file with required environment variables (see below)

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

### Admin Theme

The admin panel uses a **VS Code Light theme** with:
- White backgrounds (`#ffffff`) and light gray page background (`#f5f5f5`)
- VS Code blue accent (`#007acc`) for primary actions and focus states
- Clean borders (`#e0e0e0`, `#d0d0d0`) with subtle box shadows
- Dark text (`#333`, `#24292e`) on white editor textareas

## Security Features

- All `/admin/*` routes (except login) require authenticated session
- Passwords stored as **bcrypt hashes** (never plaintext)
- Session-based auth with `HttpOnly`, `SameSite=Strict` cookies
- **Path traversal protection** on all file operations
- **Automatic backup** before every file write (stored in `.cms-backups/`)
- AI content requires **explicit admin approval** before saving
- `.env` excluded from version control

## Website Pages

| Page | File | Description |
|------|------|-------------|
| Home | `index.html` | Main landing page |
| About | `about.html` | Company information |
| FAQ | `faq.html` | Frequently asked questions |
| Contact | `contact.html` | Contact form (mail.php backend) |
| VoIP | `voip.html` | VoIP services overview |
| Features | `features.html` | Feature breakdown |
| Clients | `clients.html` | Client showcase |
| Testimonials | `testimonials.html` | Customer testimonials |
| Videos | `videos.html` | Video gallery |
| Privacy | `privacy.html` | Privacy policy |
| Terms | `terms.html` | Terms of service |
| Phones | `phones.html` | Hardware phones catalog |
| PBX/SIP Trunk | `pbx-sip-trunk.html` | PBX and SIP trunking |
| Contact Center | `contact-center.html` | Contact center solution |
| Cloud SMS | `cloud-sms.html` | Cloud SMS service |
| E-Fax | `e-fax.html` | Electronic fax service |
| UC Client | `uc-client.html` | Unified communications client |
| Video Conferencing | `video-conferencing.html` | Video conferencing |
| Mobile App | `mobile-application.html` | Mobile application |
| Workforce Mgmt | `workforce-management.html` | Workforce management |
| API Integration | `api-integration.html` | API integration details |
| API Videos | `api-videos.html` | API demo videos |
| SMS | `sms.html` | SMS service |
| Quote | `quote.html` | Request a quote |
| Elite Plan | `elite.html` | Elite pricing plan |
| Premier Plan | `premier.html` | Premier pricing plan |

## Deployment

### Production Server

The site runs on a Linux server with Nginx as a reverse proxy:

```
Internet → Nginx (port 443/SSL) → Go webserver (port 9080)
```

### Deploy Steps

```bash
# 1. Build the binary
make build

# 2. Restart the server
kill $(pgrep -f "webserver.*9080")
nohup ./bin/webserver -port 9080 > /tmp/pcsvoip-webserver.log 2>&1 &

# 3. Verify
curl -s -o /dev/null -w "%{http_code}" http://localhost:9080/admin/login
# Should return: 200
```

### SSL / Certbot

SSL certificates are managed via Let's Encrypt certbot with automatic renewal.

## Files NOT in Git

These are excluded via `.gitignore` and must be managed separately:

| Path | Reason |
|------|--------|
| `.env` | Contains API keys and secrets |
| `bin/` | Compiled Go binaries (rebuild with `make build`) |
| `assets/apivideos/` | ~230MB of demo videos (deploy via FTP/rsync) |
| `.cms-backups/` | Runtime backup files created by the CMS |
| `node_modules/` | Test dependencies (install with `npm install`) |
| `.well-known/` | Certbot ACME challenge files |

## Maintenance

### Adding a New Page

1. Create the `.html` file in the project root
2. The static file server will automatically serve it
3. Add navigation links in the relevant templates as needed

### Changing AI Provider

Update `.env`:
```bash
AI_PROVIDER=anthropic          # or openai, grok
ANTHROPIC_API_KEY=sk-ant-...   # Key for the chosen provider
```
Restart the server to pick up changes.

### Updating Admin Templates

Templates are in `web/templates/`. After editing:
```bash
make build
# Restart server (templates are parsed at startup)
kill $(pgrep -f "webserver.*9080")
nohup ./bin/webserver -port 9080 > /tmp/pcsvoip-webserver.log 2>&1 &
```
