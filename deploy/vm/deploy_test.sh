#!/usr/bin/env bash
# deploy_test.sh: runs deploy.sh on a pretend service VM (auth jobs dispatch scheduler gateway)
# in a throwaway container, with docker, curl and swap stubbed, and checks which roles each
# mode brings up and which it stops. A gateway-only deploy must stop nothing: the clean-up
# that stops roles moved off a VM once took the step's role list for the VM's.
# Needs Docker. Run from the repository root: bash deploy/vm/deploy_test.sh
set -euo pipefail
export MSYS_NO_PATHCONV=1
src=$(pwd -W 2>/dev/null || pwd)
docker run --rm -i -v "$src/deploy":/src/deploy:ro bash:5 bash -s <<'EOF'
set -euo pipefail
cp -r /src /work
mkdir -p /etc/tq/certs /opt/tq/abc/ /etc/sysctl.d /stub
printf 'ROLES="auth jobs dispatch scheduler gateway"\nIMAGE=x\n' >/etc/tq/tq.env
touch /etc/tq/certs/ca.crt /etc/tq/certs/svc.crt /etc/tq/certs/svc.key
cat >/stub/docker <<'S'
#!/bin/sh
echo "docker $*" >>/tmp/log
case "$*" in *"compose ls -aq"*) printf 'tq-auth\ntq-jobs\ntq-dispatch\ntq-scheduler\ntq-gateway\n' ;; esac
S
printf '#!/bin/sh\ncase "$*" in *version*) echo deadbeef ;; esac\n' >/stub/curl
printf '#!/bin/sh\necho /swapfile\n' >/stub/swapon
printf '#!/bin/sh\n' >/stub/sysctl
chmod +x /stub/*
export PATH=/stub:$PATH

check() { # check <mode> <roles brought up>
  : >/tmp/log
  bash /work/deploy/vm/deploy.sh deadbeef $1 >/dev/null
  local up down
  up=$(grep -o 'tq-[a-z]* -f [^ ]* up' /tmp/log | cut -d' ' -f1 | sort -u | xargs)
  down=$(grep ' down' /tmp/log | grep -o 'tq-[a-z]*' | xargs || true)
  [ "$up" = "$2" ] || { echo "FAIL ${1:-full}: brought up [$up], want [$2]"; exit 1; }
  [ -z "$down" ] || { echo "FAIL ${1:-full}: stopped [$down], want nothing"; exit 1; }
  echo "ok ${1:-full}: up [$up], stopped nothing"
}
check "" "tq-auth tq-dispatch tq-gateway tq-jobs tq-scheduler"
check gateway "tq-gateway"
EOF
