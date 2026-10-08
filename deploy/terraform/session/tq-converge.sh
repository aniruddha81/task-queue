#!/usr/bin/env bash
# tq-converge [sha [step]]: fetch this VM's files (certificates, WireGuard, settings), bring
# up the mesh, then run release <sha>'s own deploy/vm/deploy.sh. Without a sha, converge to
# the recorded current release. Installed by cloud-init; it knows no version.
set -euo pipefail
export PATH=$PATH:/snap/bin
exec 9>/run/tq-converge.lock && flock 9 # boot and a rollout never converge at once
. /etc/tq/vm.env
umask 077

if [ "$CLOUD" = aws ]; then
  files=$(aws ssm get-parameters-by-path --region "$SSM_REGION" --path "/tq/vm/$VM_NAME/" \
    --recursive --with-decryption --output json |
    jq --arg p "/tq/vm/$VM_NAME/" '[.Parameters[] | {key: (.Name | ltrimstr($p)), value: .Value}] | from_entries')
  current() { aws ssm get-parameter --region "$SSM_REGION" --name /tq/release/current --query Parameter.Value --output text; }
else
  token=$(curl -fsS -H Metadata:true \
    "http://169.254.169.254/metadata/identity/oauth2/token?api-version=2018-02-01&resource=https://vault.azure.net" | jq -r .access_token)
  kv() { curl -fsS -H "Authorization: Bearer $token" "https://$VAULT.vault.azure.net/secrets/$1?api-version=7.4" | jq -r .value; }
  files=$(kv "vm-$VM_NAME")
  current() { kv release-current; }
fi
[ "$(jq length <<<"$files")" -gt 0 ] || { echo "no files for $VM_NAME yet"; exit 1; }
for f in $(jq -r 'keys[]' <<<"$files"); do
  mkdir -p "/etc/tq/$(dirname "$f")"
  jq -j --arg f "$f" '.[$f]' <<<"$files" >"/etc/tq/$f.new" && mv "/etc/tq/$f.new" "/etc/tq/$f"
done
# Containers run as non-root users and read their certificates (and Prometheus its config)
# through bind mounts; the host's other users can't get past /etc/tq.
chmod 0750 /etc/tq && chmod 0755 /etc/tq/certs && chmod 0644 /etc/tq/certs/* /etc/tq/prometheus.yml 2>/dev/null || true

# Mesh: names, then WireGuard (a reload applies peer changes without dropping the tunnel).
sed -i '/# tq-begin/,/# tq-end/d' /etc/hosts && cat /etc/tq/hosts >>/etc/hosts
install -m 600 /etc/tq/wg0.conf /etc/wireguard/wg0.conf
systemctl enable -q wg-quick@wg0
systemctl reload-or-restart wg-quick@wg0

sha=${1:-$(current 2>/dev/null || true)}
case "$sha" in "" | none) echo "no release recorded yet; mesh is up"; exit 0 ;; esac
src=/opt/tq/$sha
umask 022 # the release's files are config that containers read as non-root users
if [ ! -f "$src/deploy/vm/deploy.sh" ]; then
  rm -rf "$src.tmp" && mkdir -p "$src.tmp"
  curl -fsSL "https://codeload.github.com/$REPO/tar.gz/$sha" | tar xz -C "$src.tmp" --strip-components=1
  mv "$src.tmp" "$src"
fi
exec bash "$src/deploy/vm/deploy.sh" "$sha" "${2:-}"
