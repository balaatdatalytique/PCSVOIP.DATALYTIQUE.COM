# GCP Migration Plan — PCS VoIP / Pegasi AI Platform

**Prepared:** April 22, 2026
**Current Host:** srv1329845 (Hostinger VPS) — 76.13.124.114
**Target:** Google Cloud Platform

---

## 1. Current Infrastructure Summary

### Server Specs
| Resource | Current |
|---|---|
| CPU | AMD EPYC 9354P, 4 cores |
| RAM | 15 GB |
| Disk | 197 GB (47 GB used) |
| OS | Debian 13 (Linux 6.12) |
| IP | 76.13.124.114 (static) |

### Containers (10 running)

| Container | Domain | Port | Image Source | Data |
|---|---|---|---|---|
| nginx-proxy | All domains (SSL) | 80/443 | `nginx:alpine` | nginx.conf, certs |
| certbot | — | — | `certbot/certbot` | Let's Encrypt certs |
| freeswitch | — (host network) | 5060,5080,8021,8082 | Harbor registry | 1.7 GB config+data |
| smartcrm-postgres | — | 5432 | `postgres:16-alpine` | 66 MB database |
| pcsvoip-web | pcsvoip.datalytique.com | 9080 | Built from source | 1.8 MB admin DB |
| pcsvoip-voice | pcsvoip.datalytique.com | 9081 | Built from source | Stateless |
| smartcrm | crm.pegasiai.com | 9005 | Harbor tag :80 | 4 MB bbolt DB |
| pegasiai-smartcrm | www.pegasiai.com | 9006 | Harbor tag :54 | 2 MB bbolt DB |
| pcs-autoattender | pcs.autoattender.ai | 9011 | Harbor tag :54 | 256 KB bbolt DB |
| autoattender-website | www.autoattender.ai | 9012 | Built from source | 128 KB bbolt DB |

### Domains & SSL
All domains use Let's Encrypt certificates managed by Certbot:
1. `pcsvoip.datalytique.com`
2. `crm.pegasiai.com`
3. `www.pegasiai.com` / `pegasiai.com`
4. `pcs.autoattender.ai`
5. `www.autoattender.ai`
6. `demo.incidentshub.io`

### External Dependencies
| Service | Used By |
|---|---|
| CoreDial SIP trunk (`a2east.sipregistration.com`) | FreeSWITCH outbound calls |
| Grok/xAI API | AI voice + text chat |
| HostMonster SMTP (`host2012.hostmonster.com`) | Quote form + transcript emails |
| Zendesk | Support tickets |
| Harbor registry (`harbor.illumate.ai`) | SmartCRM + FreeSWITCH images |
| GitHub | Source code (3 repos) |

### Source Code Repositories
| Repo | Containers | Branch |
|---|---|---|
| `github.com:balaatdatalytique/PCSVOIP.DATALYTIQUE.COM.git` | pcsvoip-web, pcsvoip-voice | main |
| `github.com:DATALYTIQUE-INC/VOICE-CAMPAIGN.git` | smartcrm (CRM) | main |
| `github.com:DATALYTIQUE-INC/AUTOATTENDER.git` | autoattender-website | main |
| Harbor images (no source on server) | pegasiai-smartcrm, pcs-autoattender, freeswitch | — |

---

## 2. Recommended GCP Architecture

### Option A: Single VM (Lift-and-Shift) — Recommended for Phase 1

Mirrors the current setup on a single Compute Engine VM. Fastest path to production.

```
GCP Compute Engine VM (e2-standard-4: 4 vCPU, 16 GB RAM)
├── Docker Compose (same as current)
├── 100 GB SSD boot disk
├── Static external IP
├── Firewall rules for SIP/HTTP/HTTPS
└── Cloud DNS for all domains
```

**Estimated Monthly Cost:**
| Resource | Spec | Cost/mo |
|---|---|---|
| Compute Engine | e2-standard-4 (4 vCPU, 16 GB) | ~$100 |
| Boot disk | 100 GB SSD | ~$17 |
| Static IP | 1 external IP | ~$3 |
| Egress | ~50 GB/mo estimated | ~$5 |
| Cloud DNS | 6 zones | ~$3 |
| **Total** | | **~$128/mo** |

### Option B: GKE (Kubernetes) — Future Phase 2

For scaling, HA, and auto-healing. Only pursue after Phase 1 is stable.

```
GKE Autopilot Cluster
├── Ingress Controller (replaces nginx-proxy)
├── Cert-Manager (replaces certbot)
├── Cloud SQL for PostgreSQL (replaces smartcrm-postgres)
├── Persistent Volumes for bbolt DBs
├── FreeSWITCH as DaemonSet with hostNetwork
└── Artifact Registry (replaces Harbor)
```

**Estimated Monthly Cost:** ~$250–350/mo (includes GKE management fee, Cloud SQL, etc.)

---

## 3. Phase 1: Lift-and-Shift Migration Steps

### Step 1: Provision GCP Infrastructure (Day 1)

```bash
# Create project
gcloud projects create pcsvoip-production --name="PCS VoIP Production"
gcloud config set project pcsvoip-production

# Enable APIs
gcloud services enable compute.googleapis.com dns.googleapis.com

# Create VM
gcloud compute instances create pcsvoip-prod \
  --zone=us-central1-a \
  --machine-type=e2-standard-4 \
  --image-family=debian-12 \
  --image-project=debian-cloud \
  --boot-disk-size=100GB \
  --boot-disk-type=pd-ssd \
  --tags=http-server,https-server,sip-server

# Reserve static IP
gcloud compute addresses create pcsvoip-ip --region=us-central1
# Note the IP for DNS updates

# Firewall rules
gcloud compute firewall-rules create allow-http \
  --allow=tcp:80,tcp:443 --target-tags=http-server

gcloud compute firewall-rules create allow-sip \
  --allow=tcp:5060,tcp:5066,tcp:5080,udp:5060,udp:5080,tcp:7443,tcp:8021,tcp:8082 \
  --target-tags=sip-server

# RTP port range for FreeSWITCH
gcloud compute firewall-rules create allow-rtp \
  --allow=udp:16384-32768 \
  --target-tags=sip-server
```

### Step 2: Install Docker on GCP VM (Day 1)

```bash
# SSH into new VM
gcloud compute ssh pcsvoip-prod --zone=us-central1-a

# Install Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
sudo systemctl enable docker

# Install Docker Compose
sudo apt-get install docker-compose-plugin

# Create directory structure
sudo mkdir -p /home/production/DEPLOYED
sudo chown -R $USER:$USER /home/production
```

### Step 3: Transfer Data from Current Server (Day 1-2)

Run from the **current server**:

```bash
# 1. Stop all containers to ensure data consistency
cd /home/production/DEPLOYED/smartcrm.pcsvoip.com
docker compose stop

cd /home/production/DEPLOYED/pcsvoip.datalytique.com
docker compose stop

# 2. Export Docker volumes
docker run --rm -v smartcrmpcsvoipcom_postgres-data:/data -v /tmp/backup:/backup \
  alpine tar czf /backup/postgres-data.tar.gz -C /data .

docker run --rm -v freeswitch-data:/data -v /tmp/backup:/backup \
  alpine tar czf /backup/freeswitch-data.tar.gz -C /data .

docker run --rm -v pcsvoipdatalytiquecom_admin-data:/data -v /tmp/backup:/backup \
  alpine tar czf /backup/admin-data.tar.gz -C /data .

docker run --rm -v smartcrmpcsvoipcom_certbot-etc:/data -v /tmp/backup:/backup \
  alpine tar czf /backup/certbot-etc.tar.gz -C /data .

docker run --rm -v smartcrmpcsvoipcom_freeswitch-dialplan:/data -v /tmp/backup:/backup \
  alpine tar czf /backup/freeswitch-dialplan.tar.gz -C /data .

# 3. Dump PostgreSQL
docker exec smartcrm-postgres pg_dumpall -U smartcrm > /tmp/backup/postgres-dump.sql

# 4. Transfer deployed code + data + volumes to GCP
GCP_IP="<new-gcp-ip>"
rsync -avz --progress /home/production/DEPLOYED/ $GCP_IP:/home/production/DEPLOYED/
rsync -avz --progress /tmp/backup/ $GCP_IP:/tmp/backup/
```

### Step 4: Restore on GCP VM (Day 2)

```bash
# 1. Clone source repos (for locally-built images)
cd /home/production/DEPLOYED/pcsvoip.datalytique.com
git remote set-url origin git@github.com:balaatdatalytique/PCSVOIP.DATALYTIQUE.COM.git

# 2. Login to Harbor registry
docker login harbor.illumate.ai

# 3. Pull Harbor images
docker pull harbor.illumate.ai/datalytique/smartcrm:80
docker pull harbor.illumate.ai/datalytique/smartcrm:60
docker pull harbor.illumate.ai/datalytique/freeswitch:latest

# 4. Create Docker volumes
docker volume create smartcrmpcsvoipcom_postgres-data
docker volume create freeswitch-data
docker volume create pcsvoipdatalytiquecom_admin-data
docker volume create smartcrmpcsvoipcom_certbot-etc
docker volume create smartcrmpcsvoipcom_freeswitch-dialplan

# 5. Restore volume data
for vol in postgres-data freeswitch-data admin-data certbot-etc freeswitch-dialplan; do
  docker run --rm -v smartcrmpcsvoipcom_${vol}:/data -v /tmp/backup:/backup \
    alpine sh -c "cd /data && tar xzf /backup/${vol}.tar.gz"
done

# 6. Restore PostgreSQL
cd /home/production/DEPLOYED/smartcrm.pcsvoip.com
docker compose up -d smartcrm-postgres
sleep 5
docker exec -i smartcrm-postgres psql -U smartcrm < /tmp/backup/postgres-dump.sql

# 7. Start infrastructure (nginx, freeswitch, certbot)
docker compose up -d

# 8. Start applications
cd /home/production/DEPLOYED/pcsvoip.datalytique.com
docker compose up -d --build

cd /home/production/DEPLOYED/crm.pegasiai.com
docker compose up -d

cd /home/production/DEPLOYED/www.pegasiai.com
docker compose up -d

cd /home/production/DEPLOYED/pcs.autoattender.ai
docker compose up -d

cd /home/production/DEPLOYED/www.autoattender.ai
docker compose up -d
```

### Step 5: Update DNS (Day 2-3)

Update DNS records for all domains to point to the new GCP static IP.

| Domain | Record | Old Value | New Value |
|---|---|---|---|
| pcsvoip.datalytique.com | A | 76.13.124.114 | `<GCP_IP>` |
| crm.pegasiai.com | A | 76.13.124.114 | `<GCP_IP>` |
| www.pegasiai.com | A | 76.13.124.114 | `<GCP_IP>` |
| pegasiai.com | A | 76.13.124.114 | `<GCP_IP>` |
| pcs.autoattender.ai | A | 76.13.124.114 | `<GCP_IP>` |
| www.autoattender.ai | A | 76.13.124.114 | `<GCP_IP>` |
| demo.incidentshub.io | A | 76.13.124.114 | `<GCP_IP>` |

**DNS TTL Strategy:**
1. 48 hours before migration: lower TTL to 300 seconds (5 min)
2. After migration: update A records to new IP
3. 48 hours after stable: restore TTL to 3600 seconds

### Step 6: Re-issue SSL Certificates (Day 3)

```bash
# After DNS propagation, re-issue certificates
docker exec certbot certbot certonly --webroot \
  -w /var/www/certbot \
  -d pcsvoip.datalytique.com \
  -d crm.pegasiai.com \
  -d www.pegasiai.com \
  -d pegasiai.com \
  -d pcs.autoattender.ai \
  --non-interactive --agree-tos

# Reload nginx
docker exec nginx-proxy nginx -s reload
```

### Step 7: Update FreeSWITCH SIP Registration (Day 3)

FreeSWITCH `external_rtp_ip` and `external_sip_ip` must point to the new GCP IP. Update in the FreeSWITCH config or via `ext-rtp-ip` and `ext-sip-ip` settings.

Also update the CoreDial SIP trunk registration if the trunk is IP-authenticated (whitelist the new GCP IP with CoreDial).

### Step 8: Validation Checklist (Day 3-4)

| Test | Command / Action | Expected |
|---|---|---|
| Website loads | `curl -I https://pcsvoip.datalytique.com` | 200 OK |
| Text chatbot | Send message in chatbot | AI response |
| Voice chatbot | Click mic in chatbot | Voice session starts |
| Quote form | Submit quote form | Email received at Sales@pcsusa.com |
| AI callback | Request callback from chatbot | Phone rings, Aria speaks |
| Appointment booking | Ask Aria to schedule a demo | Appointment in CRM |
| Transcript email | Hang up callback | Email with transcript |
| CRM access | Login at crm.pegasiai.com | Dashboard loads |
| SIP registration | `fs_cli -x "sofia status"` | coredial_east REGED |
| SSL certificates | Browser padlock icon | Valid cert |
| All healthchecks | `./healthcheck.sh` | All pass |

---

## 4. Configuration Changes Required on GCP

### Environment Variables
Update in `.env` files:
- `PUBLIC_BASE_URL` — same domains, no change needed
- `FREESWITCH_HOST` — change from `host.docker.internal` to `host.docker.internal` (works with `extra_hosts` in compose)
- `FREESWITCH_DOMAIN` — may need to update to GCP IP for SIP registration

### FreeSWITCH
- Update `ext-rtp-ip` and `ext-sip-ip` to GCP external IP (or use `auto-nat`)
- Update SIP trunk provider (CoreDial) with new IP for registration/ACL
- Verify RTP port range is open in GCP firewall (UDP 16384-32768)

### SMTP
- No changes needed (HostMonster SMTP is external)
- Verify outbound port 587 is not blocked by GCP (it isn't by default)

---

## 5. Rollback Plan

If issues arise after DNS cutover:

1. **Immediate (0-5 min):** Revert DNS A records to 76.13.124.114
2. **Short-term:** Keep the old server running for 2 weeks after migration
3. **Data sync:** If rollback needed after data diverges, re-dump PostgreSQL and bbolt DBs from GCP back to old server

---

## 6. Data Inventory for Migration

| Data Type | Size | Migration Method |
|---|---|---|
| Application code | 3 GB | rsync + git clone |
| Docker volumes | 1.8 GB | tar + rsync |
| PostgreSQL | 66 MB | pg_dumpall |
| BBolt databases | ~10 MB total | Included in volumes/rsync |
| SSL certificates | 272 KB | Volume backup or re-issue |
| FreeSWITCH config | 1.7 GB | Volume backup |
| nginx.conf | 11 KB | Included in rsync |
| .env files | ~10 KB | Included in rsync |
| **Total transfer** | **~7 GB** | |

---

## 7. Timeline

| Day | Task | Duration |
|---|---|---|
| Day 1 | Provision GCP VM, install Docker, transfer data | 4-6 hours |
| Day 2 | Restore data, build images, start services, smoke test | 4-6 hours |
| Day 2 | Lower DNS TTL (48h before cutover) | 5 min |
| Day 3 | Update DNS, re-issue SSL certs, update SIP trunk | 2-3 hours |
| Day 3-4 | Full validation testing | 2-3 hours |
| Day 4-7 | Monitor, fix any issues | Ongoing |
| Day 14 | Decommission old server | 1 hour |

**Total estimated effort: 2-3 days active work, 2 weeks monitoring.**

---

## 8. Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| DNS propagation delay | Users hit old server | Lower TTL 48h before, keep old server running |
| SIP trunk IP change | Outbound calls fail | Pre-register new IP with CoreDial, test before DNS cut |
| FreeSWITCH RTP blocked | No audio on calls | Open UDP 16384-32768 in GCP firewall |
| Harbor registry access | Can't pull images | Pre-pull images on old server, `docker save/load` |
| GCP outbound port 25 blocked | SMTP fails | We use port 587 (STARTTLS), not blocked |
| Data loss during transfer | DB inconsistency | Stop containers before export, verify checksums |

---

## 9. Post-Migration Optimization (Phase 2)

Once stable on GCP, consider:

1. **Cloud SQL** — Migrate PostgreSQL to Cloud SQL for managed backups, HA, scaling
2. **Artifact Registry** — Push images to GCP Artifact Registry instead of Harbor
3. **Cloud Monitoring** — Set up uptime checks, alerting, log aggregation
4. **Persistent Disk snapshots** — Automated daily snapshots for disaster recovery
5. **Load Balancer** — Replace nginx-proxy with GCP HTTPS Load Balancer for global CDN + DDoS protection
6. **GKE migration** — Containerize into Kubernetes for auto-scaling and zero-downtime deploys
7. **Cloud Armor** — WAF protection for public endpoints
8. **Secret Manager** — Move .env credentials to GCP Secret Manager
