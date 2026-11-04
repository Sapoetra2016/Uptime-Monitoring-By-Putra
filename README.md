# Uptime Monitoring — Panduan Instalasi & Deployment

Aplikasi monitoring uptime/self-hosted (terinspirasi [Uptime Kuma](https://github.com/louislam/uptime-kuma)) dibangun dengan **Go** (backend) + **React** (frontend) + **PostgreSQL**, dengan update **realtime via WebSocket** (tanpa reload).

Dokumen ini adalah tutorial instalasi lengkap. Semua perintah di bawah adalah perintah nyata yang dipakai [`deploy.sh`](./deploy.sh) — Anda bisa menjalankannya **otomatis** lewat script, atau **manual** langkah demi langkah sesuai panduan ini.

---

## Daftar isi

- [Fitur](#fitur)
- [Arsitektur](#arsitektur)
- [Kebutuhan sistem](#kebutuhan-sistem)
- [Cara A — Instalasi otomatis (deploy.sh)](#cara-a--instalasi-otomatis-deploysh)
- [Cara B — Instalasi manual (langkah demi langkah)](#cara-b--instalasi-manual-langkah-demi-langkah)
- [Variabel environment](#variabel-environment)
- [Konfigurasi nginx & systemd](#konfigurasi-nginx--systemd)
- [Setelah instalasi (hardening)](#setelah-instalasi-hardening)
- [Update / redeploy](#update--redeploy)
- [Perintah berguna](#perintah-berguna)
- [Mengaktifkan HTTPS](#mengaktifkan-https)
- [Pemecahan masalah](#pemecahan-masalah)
- [Development lokal](#development-lokal)

---

## Fitur

- **Realtime penuh tanpa reload** — status, latency, dan insiden di-push lewat WebSocket; dashboard & halaman status ter-update sendiri.
- **Banyak tipe monitor**: HTTP (+keyword/status code), TCP, DNS lookup, TLS certificate expiry, Push/Heartbeat, Docker container, Ping, Redis, Database (Postgres/MySQL).
- **Notifikasi & escalation bertingkat** — Telegram / Discord / Slack / Webhook / Email, dengan kebijakan eskalasi berdasarkan lama downtime.
- **Analitik** — availability %, latency avg + p50/p95/p99, grafik, riwayat check.
- **Status page publik** (`/status/{slug}`) dan **daftar monitor publik** tanpa login.
- **SLO/SLA target** per monitor, filter status, pause/resume, audit "siapa menambahkan" monitor.
- **RBAC** — admin (bisa kelola) & viewer (read-only).

## Arsitektur

```
.
├── backend/            Go, chi router, pgx, gorilla/websocket  (module: github.com/uptime/monitoring)
│   ├── cmd/server      entrypoint
│   └── internal/
│       ├── config      konfigurasi berbasis env (UPTIME_*)
│       ├── db          koneksi + auto-migrate skema
│       ├── models      tipe domain (Monitor, CheckResult, Notification, ...)
│       ├── store       repository PostgreSQL (CRUD, query stats/percentile)
│       ├── monitor     engine check: HTTP/TCP/DNS/Cert/Push/Docker/Ping/Redis/DB + scheduler
│       ├── events      WebSocket hub (fan-out realtime)
│       ├── notifier    Telegram/Discord/Slack/Webhook/Email + loop escalation
│       ├── auth        hash password (bcrypt) + bearer token HMAC
│       └── api         REST endpoint + WebSocket + status page publik
├── frontend/           React + Vite + TailwindCSS + Recharts
│   └── src/            lib/ hooks/ components/ pages/
└── deploy.sh           script instalasi bare-metal (Ubuntu/Debian)
```

**Alur produksi:** Nginx menyajikan frontend statis dari `/var/www/uptime`, dan me-reverse-proxy `/api/` (termasuk `/api/ws` untuk WebSocket) ke backend Go di `127.0.0.1:8080`. Backend dijalankan sebagai service `systemd` (`uptime.service`) dan menyimpan data di PostgreSQL.

## Kebutuhan sistem

- Server **Ubuntu 22.04/24.04** atau **Debian 12** (x86_64 atau arm64) dengan akses **root/sudo**.
- Port **80** (dan **443** untuk HTTPS) terbuka.
- Paket yang akan dipasang otomatis: PostgreSQL 16, Go 1.25, Node.js 20, Nginx, UFW.

---

## Cara A — Instalasi otomatis (deploy.sh)

Cara tercepat. Script akan memasang semua dependency, menyiapkan database, build backend & frontend, mengonfigurasi Nginx + systemd + firewall.

**1. Salin project ke server** (mis. lewat git atau scp):

```bash
# contoh via scp dari mesin lokal Anda
scp -r ./new-fosst root@IP-SERVER:/opt/uptime-src
```

**2. Sesuaikan konfigurasi** di bagian atas `deploy.sh` (terutama password DB & admin):

```bash
nano /opt/uptime-src/deploy.sh
```

```bash
UPTIME_DOMAIN=""                    # kosongkan jika akses via IP
UPTIME_DB_USER="apps"
UPTIME_DB_PASS="ganti-password-ini" # WAJIB diganti
UPTIME_DB_NAME="Monitoring"
UPTIME_ADMIN_USER="admin"
UPTIME_ADMIN_PASS="ganti-password-ini" # WAJIB diganti
UPTIME_PORT="8080"
UPTIME_ALLOWED_ORIGINS="*"          # set ke domain Anda di produksi
INSTALL_DIR="/opt/uptime"
```

**3. Jalankan script:**

```bash
cd /opt/uptime-src
chmod +x deploy.sh
sudo ./deploy.sh
```

**4. Buka di browser:** `http://IP-SERVER` (atau `http://DOMAIN_ANDA`). Login dengan `UPTIME_ADMIN_USER` / `UPTIME_ADMIN_PASS`.

> Script ini **idempotent** — aman dijalankan ulang; user/database yang sudah ada tidak dibuat ganda.

---

## Cara B — Instalasi manual (langkah demi langkah)

Ikuti ini jika ingin memahami / mengontrol tiap tahap. Semua dijalankan sebagai **root** (atau `sudo`).

### Langkah 1 — Pasang paket sistem

```bash
apt-get update -qq
apt-get install -y curl wget git gnupg lsb-release build-essential nginx ufw
apt-get install -y postgresql-16 postgresql-client-16
# jika postgresql-16 tidak ada di repo distro Anda:
# apt-get install -y postgresql postgresql-client
```

### Langkah 2 — Pasang Go 1.25

```bash
ARCH=$(uname -m)
case $ARCH in
  x86_64)  GOARCH="amd64" ;;
  aarch64) GOARCH="arm64" ;;
  *)       GOARCH="amd64" ;;
esac
wget -q "https://go.dev/dl/go1.25.0.linux-${GOARCH}.tar.gz" -O /tmp/go.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/golang.sh
source /etc/profile.d/golang.sh
go version   # harus go1.25.x
```

### Langkah 3 — Pasang Node.js 20

```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
apt-get install -y nodejs
node --version   # v20.x
```

### Langkah 4 — Siapkan PostgreSQL

```bash
systemctl enable --now postgresql

# buat user & database (GANTI nama/password sesuai konfigurasi Anda)
sudo -u postgres psql -c "CREATE USER \"apps\" WITH PASSWORD 'ganti-password-ini';"
sudo -u postgres psql -c "CREATE DATABASE \"Monitoring\" OWNER \"apps\";"
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE \"Monitoring\" TO \"apps\";"
```

String koneksi yang akan dipakai backend:

```
postgres://apps:ganti-password-ini@127.0.0.1:5432/Monitoring?sslmode=disable
```

### Langkah 5 — Build backend & frontend

```bash
INSTALL_DIR="/opt/uptime"
mkdir -p "$INSTALL_DIR"
# salin source backend/ dan frontend/ ke $INSTALL_DIR terlebih dahulu
cp -r ./backend ./frontend "$INSTALL_DIR/"

# --- backend (Go) ---
cd "$INSTALL_DIR/backend"
export PATH=$PATH:/usr/local/go/bin
export GOTOOLCHAIN=local
go mod download
go build -o "$INSTALL_DIR/uptime-server" ./cmd/server

# --- frontend (Vite) ---
cd "$INSTALL_DIR/frontend"
npm ci
npm run build
mkdir -p /var/www/uptime
cp -r dist/* /var/www/uptime/
```

Hasil: binary `$INSTALL_DIR/uptime-server` dan aset statis di `/var/www/uptime`.

### Langkah 6 — Buat service systemd

Buat file `/etc/systemd/system/uptime.service`:

```bash
cat > /etc/systemd/system/uptime.service << 'EOF'
[Unit]
Description=Uptime Monitoring Server
After=postgresql.service
Requires=postgresql.service

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/uptime
ExecStart=/opt/uptime/uptime-server
Restart=always
RestartSec=5
Environment=UPTIME_DB_URL=postgres://apps:ganti-password-ini@127.0.0.1:5432/Monitoring?sslmode=disable
Environment=UPTIME_JWT_SECRET=GANTI-DENGAN-RANDOM-64-HEX
Environment=UPTIME_ADMIN_USER=admin
Environment=UPTIME_ADMIN_PASSWORD=ganti-password-ini
Environment=UPTIME_PORT=8080
Environment=UPTIME_ALLOWED_ORIGINS=*

[Install]
WantedBy=multi-user.target
EOF
```

Generate JWT secret random, lalu tempel ke `UPTIME_JWT_SECRET`:

```bash
openssl rand -hex 32
```

Aktifkan & jalankan:

```bash
systemctl daemon-reload
systemctl enable --now uptime
systemctl status uptime --no-pager
```

> Skema database dibuat **otomatis** saat service pertama kali start (auto-migrate). Tidak perlu menjalankan migration manual.

### Langkah 7 — Konfigurasi Nginx

Buat file `/etc/nginx/sites-available/uptime` (lihat [Konfigurasi nginx](#konfigurasi-nginx--systemd) untuk isi lengkap), lalu:

```bash
ln -sf /etc/nginx/sites-available/uptime /etc/nginx/sites-enabled/uptime
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl reload nginx
```

### Langkah 8 — Firewall

```bash
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
# ufw enable   # aktifkan hati-hati, pastikan 22 sudah diizinkan
```

### Selesai

Buka `http://IP-SERVER`. Login dengan akun admin. Lanjut ke [hardening](#setelah-instalasi-hardening).

---

## Variabel environment

Backend membaca konfigurasi dari environment variable (prefix **`UPTIME_`**):

| Variabel | Default | Keterangan |
|---|---|---|
| `UPTIME_DB_URL` | `postgres://apps:3t13rn0c@127.0.0.1:5432/Monitoring?sslmode=disable` | String koneksi PostgreSQL |
| `UPTIME_JWT_SECRET` | `dev-secret-change-me` | Secret penandatangan token — **wajib diganti di produksi** |
| `UPTIME_ADMIN_USER` | `admin` | Username admin yang di-seed saat start pertama |
| `UPTIME_ADMIN_PASSWORD` | `admin123` | Password admin — **ganti setelah login pertama** |
| `UPTIME_PORT` | `8080` | Port API backend |
| `UPTIME_CHECK_TIMEOUT_SEC` | `10` | Timeout default probe |
| `UPTIME_ALLOWED_ORIGINS` | `*` | Origin yang diizinkan CORS/WebSocket, pisahkan dengan koma. **Sempitkan ke domain publik di produksi**, mis. `https://status.example.com` |

Variabel ini diset di file systemd (`uptime.service`). Setelah mengubahnya:

```bash
systemctl daemon-reload && systemctl restart uptime
```

---

## Konfigurasi nginx & systemd

Isi lengkap `/etc/nginx/sites-available/uptime` (SPA statis + reverse proxy API + WebSocket):

```nginx
server {
    listen 80;
    server_name _;                 # ganti ke domain Anda, mis. status.example.com

    root /var/www/uptime;
    index index.html;

    # Frontend SPA
    location / {
        try_files $uri $uri/ /index.html;
    }

    # API reverse proxy
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # WebSocket
    location /api/ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 86400;
    }

    # Cache static assets
    location /assets/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
```

> **Tips cache:** aset ber-hash (`/assets/index-XXXX.js`) di-cache 1 tahun (aman, nama file berubah tiap build). Jika `index.html` ikut ter-cache sehingga update tidak muncul, tambahkan `location = /index.html { add_header Cache-Control "no-cache"; }` dan hard-refresh browser (Ctrl+Shift+R).

Unit systemd lengkap ada di [Langkah 6](#langkah-6--buat-service-systemd).

---

## Setelah instalasi (hardening)

1. **Ganti password admin** — login → Settings → Profil & Security → ganti password.
2. **Ganti `UPTIME_JWT_SECRET`** dengan nilai acak (`openssl rand -hex 32`), lalu `systemctl daemon-reload && systemctl restart uptime`.
3. **Sempitkan `UPTIME_ALLOWED_ORIGINS`** ke domain publik Anda (jangan `*` bila API terekspos internet).
4. **Aktifkan HTTPS** (lihat [bagian HTTPS](#mengaktifkan-https)).
5. Anggap konfigurasi admin sebagai **trusted input**: probe & webhook mengirim request keluar ke URL yang diisi admin — batasi akses dashboard ke orang tepercaya.

---

## Update / redeploy

### Opsi 1 — Rebuild di server

```bash
cd /opt/uptime/backend  && go build -o /opt/uptime/uptime-server ./cmd/server
cd /opt/uptime/frontend && npm ci && npm run build && cp -r dist/* /var/www/uptime/
systemctl restart uptime && systemctl reload nginx
```

### Opsi 2 — Cross-compile dari mesin lokal (mis. Windows) lalu upload

Build binary Linux dari mesin lokal, upload binary + dist, pasang di server:

```bash
# --- di mesin lokal ---
# backend (cross-compile ke linux/amd64)
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../uptime-server-linux ./cmd/server
# frontend
cd ../frontend && npm run build
tar -czf dist.tar.gz dist

# --- upload ke server ---
scp ../uptime-server-linux user@IP-SERVER:/tmp/uptime-server
scp dist.tar.gz user@IP-SERVER:/tmp/dist.tar.gz

# --- di server (sebagai root) ---
# PENTING: binary yang sedang berjalan tidak bisa ditimpa -> stop dulu
systemctl stop uptime
cp /tmp/uptime-server /opt/uptime/uptime-server && chmod +x /opt/uptime/uptime-server
systemctl start uptime

# deploy frontend (ekstrak ke direktori bersih untuk hindari file basi)
mkdir -p /tmp/ddnew && tar -xzf /tmp/dist.tar.gz -C /tmp/ddnew
cp -r /tmp/ddnew/dist/* /var/www/uptime/
```

> **Catatan penting:** menaruh binary baru di disk **tidak** mempengaruhi proses yang sedang jalan — wajib `systemctl restart uptime`. Tanpa restart, endpoint/route baru akan 404. Setelah deploy frontend, **hard-refresh** browser (Ctrl+Shift+R) agar bundle JS baru termuat.

---

## Perintah berguna

```bash
systemctl status uptime          # status backend
systemctl restart uptime         # restart backend
journalctl -u uptime -f          # log backend realtime
journalctl -u uptime --since "10 min ago"

systemctl status nginx           # status nginx
nginx -t                         # cek syntax config nginx

sudo -u postgres psql "Monitoring"   # masuk shell DB
```

---

## Mengaktifkan HTTPS

Dengan Let's Encrypt (butuh domain yang sudah mengarah ke IP server):

```bash
apt install -y certbot python3-certbot-nginx
certbot --nginx -d status.example.com
```

Certbot akan otomatis mengubah config Nginx untuk HTTPS + redirect. Pastikan `UPTIME_ALLOWED_ORIGINS` diset ke `https://status.example.com`.

---

## Pemecahan masalah

| Gejala | Kemungkinan penyebab | Solusi |
|---|---|---|
| `Text file busy` saat `cp` binary | Service masih menjalankan binary | `systemctl stop uptime` dulu, baru `cp`, lalu `start` |
| Route/API baru **404** padahal binary baru | Proses lama masih jalan | `systemctl restart uptime` |
| Update frontend tidak muncul | Cache browser | Hard-refresh (Ctrl+Shift+R) / buka di jendela Incognito |
| Backend crash-loop / `auth failed` | Password DB di service ≠ di PostgreSQL | Samkan `UPTIME_DB_URL` dengan kredensial DB, `daemon-reload && restart` |
| `/api/health` error tapi web terbuka | Backend belum siap / DB down | `journalctl -u uptime -f`, cek `systemctl status postgresql` |
| WebSocket tidak konek ("offline") | Proxy `/api/ws` kurang config | Pastikan blok `location /api/ws` (Upgrade/Connection header) ada |
| `go: command not found` | PATH Go belum aktif di shell | `source /etc/profile.d/golang.sh` atau pakai `/usr/local/go/bin/go` |

---

## Development lokal

Menjalankan tanpa Nginx/systemd untuk pengembangan:

```bash
# 1. PostgreSQL (lokal atau via docker)
export UPTIME_DB_URL="postgres://apps:3t13rn0c@127.0.0.1:5432/Monitoring?sslmode=disable"

# 2. Backend (terminal 1)
cd backend
go mod tidy
go run ./cmd/server            # API di :8080

# 3. Frontend (terminal 2)
cd frontend
npm install
npm run dev                    # Vite dev server di :3000, proxy /api -> :8080
```

Buka `http://localhost:3000`. Login dengan `admin` / `admin123` (default dev — **jangan dipakai di produksi**).

---

© Putra_aja
