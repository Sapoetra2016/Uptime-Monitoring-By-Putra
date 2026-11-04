#!/bin/bash
# ============================================================
# Uptime Monitoring - Bare-metal deploy script (Ubuntu/Debian)
# ============================================================
# Jalankan di server sebagai root atau dengan sudo:
#   chmod +x deploy.sh && sudo ./deploy.sh
#
# Script ini akan:
#   1. Install PostgreSQL 16, Go 1.25, Node.js 20, Nginx
#   2. Setup database + user
#   3. Build backend (Go) dan frontend (Vite)
#   4. Configure Nginx (static + reverse proxy + WebSocket)
#   5. Setup systemd service agar backend auto-start
#   6. Configure firewall (UFW)
# ============================================================

set -e

# ---- Konfigurasi (ubah sesuai kebutuhan) ----
UPTIME_DOMAIN=""                          # Kosongkan jika akses via IP saja
UPTIME_DB_USER="apps"
UPTIME_DB_PASS="ganti-password-db-anda"   # Isi nilai asli di deploy.local.sh (tidak di-commit)
UPTIME_DB_NAME="Monitoring"
UPTIME_JWT_SECRET="$(openssl rand -hex 32)"  # Auto-generate random secret
UPTIME_ADMIN_USER="admin"
UPTIME_ADMIN_PASS="ganti-password-admin-anda"  # Isi nilai asli di deploy.local.sh
UPTIME_PORT="8080"
UPTIME_ALLOWED_ORIGINS="*"                # Set ke domain Anda di production
INSTALL_DIR="/opt/uptime"
GO_VERSION="1.25.0"

# Muat rahasia lokal jika ada (file ini di-gitignore, tidak pernah ke GitHub)
SCRIPT_DIR_SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ -f "${SCRIPT_DIR_SELF}/deploy.local.sh" ]; then
    # shellcheck disable=SC1090
    source "${SCRIPT_DIR_SELF}/deploy.local.sh"
fi

echo "============================================"
echo "  Uptime Monitoring - Deployment Script"
echo "============================================"
echo ""
echo "Domain: ${UPTIME_DOMAIN:-'(IP-based access)'}"
echo "Install dir: ${INSTALL_DIR}"
echo ""

# ---- Step 1: System packages ----
echo "[1/7] Installing system packages..."
apt-get update -qq
apt-get install -y -qq \
    curl wget git gnupg lsb-release \
    build-essential \
    nginx \
    ufw \
    postgresql-16 postgresql-client-16 \
    > /dev/null 2>&1 || {
        # Some distros may not have postgresql-16 in default repos
        apt-get install -y -qq postgresql postgresql-client > /dev/null 2>&1
    }

echo "  ✓ System packages installed"

# ---- Step 2: Install Go ----
echo "[2/7] Installing Go ${GO_VERSION}..."
if command -v go &>/dev/null && go version | grep -q "go1\.2[5-9]"; then
    echo "  ✓ Go already installed: $(go version)"
else
    ARCH=$(uname -m)
    case $ARCH in
        x86_64) GOARCH="amd64" ;;
        aarch64) GOARCH="arm64" ;;
        *) GOARCH="amd64" ;;
    esac
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" -O /tmp/go.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/golang.sh
    source /etc/profile.d/golang.sh
    echo "  ✓ Go installed: $(/usr/local/go/bin/go version)"
fi

# ---- Step 3: Install Node.js 20 ----
echo "[3/7] Installing Node.js 20..."
if command -v node &>/dev/null && node --version | grep -qE "v(2[0-9]|[3-9][0-9])"; then
    echo "  ✓ Node.js already installed: $(node --version)"
else
    curl -fsSL https://deb.nodesource.com/setup_20.x | bash - > /dev/null 2>&1
    apt-get install -y -qq nodejs > /dev/null 2>&1
    echo "  ✓ Node.js installed: $(node --version)"
fi

# ---- Step 4: Setup PostgreSQL ----
echo "[4/7] Configuring PostgreSQL..."
systemctl enable postgresql > /dev/null 2>&1
systemctl start postgresql > /dev/null 2>&1

# Create user and database (idempotent)
sudo -u postgres psql -tc "SELECT 1 FROM pg_roles WHERE rolname='${UPTIME_DB_USER}'" | grep -q 1 || {
    sudo -u postgres psql -c "CREATE USER \"${UPTIME_DB_USER}\" WITH PASSWORD '${UPTIME_DB_PASS}';" > /dev/null
    echo "  ✓ Created DB user: ${UPTIME_DB_USER}"
}
sudo -u postgres psql -tc "SELECT 1 FROM pg_database WHERE datname='${UPTIME_DB_NAME}'" | grep -q 1 || {
    sudo -u postgres psql -c "CREATE DATABASE \"${UPTIME_DB_NAME}\" OWNER \"${UPTIME_DB_USER}\";" > /dev/null
    sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE \"${UPTIME_DB_NAME}\" TO \"${UPTIME_DB_USER}\";" > /dev/null
    echo "  ✓ Created database: ${UPTIME_DB_NAME}"
}

DB_URL="postgres://${UPTIME_DB_USER}:${UPTIME_DB_PASS}@127.0.0.1:5432/${UPTIME_DB_NAME}?sslmode=disable"

# ---- Step 5: Copy source + build ----
echo "[5/7] Building Uptime Monitoring..."
mkdir -p "${INSTALL_DIR}"

# Copy source files (assumes this script runs from the project root)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cp -r "${SCRIPT_DIR}/backend" "${INSTALL_DIR}/"
cp -r "${SCRIPT_DIR}/frontend" "${INSTALL_DIR}/"

# Build backend
cd "${INSTALL_DIR}/backend"
export PATH=$PATH:/usr/local/go/bin
export GOTOOLCHAIN=local
go mod download
go build -o "${INSTALL_DIR}/uptime-server" ./cmd/server
echo "  ✓ Backend built: ${INSTALL_DIR}/uptime-server"

# Build frontend
cd "${INSTALL_DIR}/frontend"
npm ci --silent
npm run build
mkdir -p /var/www/uptime
cp -r dist/* /var/www/uptime/
echo "  ✓ Frontend built and deployed to /var/www/uptime"

# ---- Step 6: Systemd service ----
echo "[6/7] Setting up systemd service..."

cat > /etc/systemd/system/uptime.service << EOF
[Unit]
Description=Uptime Monitoring Server
After=postgresql.service
Requires=postgresql.service

[Service]
Type=simple
User=www-data
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/uptime-server
Restart=always
RestartSec=5
Environment=UPTIME_DB_URL=${DB_URL}
Environment=UPTIME_JWT_SECRET=${UPTIME_JWT_SECRET}
Environment=UPTIME_ADMIN_USER=${UPTIME_ADMIN_USER}
Environment=UPTIME_ADMIN_PASSWORD=${UPTIME_ADMIN_PASS}
Environment=UPTIME_PORT=${UPTIME_PORT}
Environment=UPTIME_ALLOWED_ORIGINS=${UPTIME_ALLOWED_ORIGINS}

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable uptime > /dev/null 2>&1
systemctl start uptime
echo "  ✓ Uptime service running: $(systemctl is-active uptime)"

# ---- Step 7: Nginx config ----
echo "[7/7] Configuring Nginx..."

cat > /etc/nginx/sites-available/uptime << EOF
server {
    listen 80;
    $(if [ -n "$UPTIME_DOMAIN" ]; then echo "server_name ${UPTIME_DOMAIN};"; else echo "# server_name _;"; fi)

    root /var/www/uptime;
    index index.html;

    # Frontend SPA
    location / {
        try_files \$uri \$uri/ /index.html;
    }

    # API reverse proxy
    location /api/ {
        proxy_pass http://127.0.0.1:${UPTIME_PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

    # WebSocket
    location /api/ws {
        proxy_pass http://127.0.0.1:${UPTIME_PORT};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_read_timeout 86400;
    }

    # Cache static assets
    location /assets/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
EOF

ln -sf /etc/nginx/sites-available/uptime /etc/nginx/sites-enabled/uptime
rm -f /etc/nginx/sites-enabled/default
nginx -t > /dev/null 2>&1 && systemctl reload nginx
echo "  ✓ Nginx configured"

# ---- Firewall ----
if command -v ufw &>/dev/null; then
    ufw allow 22/tcp > /dev/null 2>&1
    ufw allow 80/tcp > /dev/null 2>&1
    ufw allow 443/tcp > /dev/null 2>&1
    echo "  ✓ Firewall ports opened (22, 80, 443)"
fi

# ---- Done ----
echo ""
echo "============================================"
echo "  DEPLOYMENT COMPLETE!"
echo "============================================"
echo ""
echo "  URL: http://${UPTIME_DOMAIN:-$(hostname -I | awk '{print $1}')}"
echo "  Admin login: ${UPTIME_ADMIN_USER} / ${UPTIME_ADMIN_PASS}"
echo ""
echo "  IMPORTANT - Change these after first login:"
echo "    - Admin password"
echo "    - UPTIME_JWT_SECRET in /etc/systemd/system/uptime.service"
echo ""
echo "  Useful commands:"
echo "    systemctl status uptime      # Backend status"
echo "    journalctl -u uptime -f      # Backend logs"
echo "    systemctl status nginx      # Nginx status"
echo "    sudo -u postgres psql Uptime # DB shell"
echo ""
echo "  To enable HTTPS (Let's Encrypt):"
echo "    apt install certbot python3-certbot-nginx"
echo "    certbot --nginx -d ${UPTIME_DOMAIN}"
echo ""
echo "  To update (re-run after git pull):"
echo "    cd ${INSTALL_DIR}/backend && go build -o uptime-server ./cmd/server"
echo "    cd ${INSTALL_DIR}/frontend && npm ci && npm run build && cp -r dist/* /var/www/uptime/"
echo "    systemctl restart uptime && systemctl reload nginx"
echo ""
