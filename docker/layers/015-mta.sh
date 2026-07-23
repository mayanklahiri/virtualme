#!/usr/bin/env bash
# Layer 015: dma (DragonFly Mail Agent) — unprivileged-friendly outbound MTA.
# Direct-MX delivery by default; optional smarthost; queue with retries.
# Chosen over OpenSMTPD/Postfix/Exim, which require root (container is uid 1000).
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

debconf-set-selections <<'EOF'
dma dma/mailname string virtualme.local
dma dma/relayhost string
EOF

apt-get update
apt-get install -y --no-install-recommends dma
rm -rf /var/lib/apt/lists/*
dpkg -s dma | grep '^Version:'

chmod 0755 /usr/sbin/dma
rm -f /etc/cron.d/dma

rm -f /etc/dma/dma.conf /etc/dma/auth.conf
ln -s /home/virtualme/.virtualme/mail/dma.conf /etc/dma/dma.conf
ln -s /home/virtualme/.virtualme/mail/auth.conf /etc/dma/auth.conf
rm -rf /var/spool/dma
ln -s /home/virtualme/.virtualme/mail/spool /var/spool/dma
